package agentchain

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/gogoproto/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/chain"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/mcpcodec"
)

// restClient is the same three chain surfaces Dial bundles, reached over the
// chain's Cosmos REST API (the gRPC-gateway, typically :1317) instead of
// gRPC. Paths mirror the google.api.http annotations compiled into the
// modules' query.pb.gw.go.
//
// It exists because a registration is signed HERE, on the operator's machine,
// and the chain's gRPC port is often the one thing a node does not expose past
// its own host. Its REST port frequently is — behind the same reverse proxy
// that fronts everything else — and the svpchain-research-agent already
// reaches x/agent that way, so this is the same route, not a new one.
type restClient struct {
	base     string
	http     *http.Client
	cdc      codec.Codec
	registry codectypes.InterfaceRegistry
}

// DialREST builds a Client for the chain carrying x/agent, over its REST base
// URL (scheme://host[:port], a trailing slash tolerated). It does not touch
// the network: the first query does, and reports the URL if it fails.
func DialREST(baseURL string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("REST url %q: want scheme://host[:port]", baseURL)
	}
	enc := mcpcodec.GetEncodingConfig()
	r := &restClient{
		base:     baseURL,
		http:     &http.Client{Timeout: 15 * time.Second},
		cdc:      enc.Codec,
		registry: enc.InterfaceRegistry,
	}
	return &Client{
		close:     func() error { r.http.CloseIdleConnections(); return nil },
		agents:    r,
		accounts:  r,
		broadcast: r,
	}, nil
}

// get fetches path (already escaped) and proto-JSON-decodes the body into out.
func (r *restClient) get(ctx context.Context, path string, out proto.Message) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+path, nil)
	if err != nil {
		return err
	}
	return r.do(req, out)
}

func (r *restClient) do(req *http.Request, out proto.Message) error {
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("chain REST %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("chain REST %s: read body: %w", req.URL.Path, err)
	}
	if resp.StatusCode != http.StatusOK {
		// The gateway wraps gRPC errors as {"code":n,"message":"..."}. Surfaced
		// as a gRPC status so callers ask one question — status.Code — of
		// either transport. Only a body carrying a code is mapped: a bare 404
		// from a proxy that does not know the path must not read as NotFound,
		// or a wrong URL would register a second copy of an existing agent.
		var ge struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &ge) == nil && ge.Message != "" && ge.Code != 0 {
			return status.Errorf(codes.Code(ge.Code), "chain REST %s: %s (HTTP %d)",
				req.URL.Path, ge.Message, resp.StatusCode)
		}
		return fmt.Errorf("chain REST %s: HTTP %d", req.URL, resp.StatusCode)
	}
	if err := r.cdc.UnmarshalJSON(body, out); err != nil {
		return fmt.Errorf("chain REST %s: decode response: %w", req.URL.Path, err)
	}
	return nil
}

// -- x/agent (agentQuerier) ----------------------------------------------
//
// Paths follow proto/dydxprotocol/agent/query.proto's google.api.http
// options — the proto, not the generated query.pb.gw.go, which lags it (it
// still binds Agent at /dydxprotocol/agent/{agent_id}, a route the running
// chain answers with 501 Unimplemented).

func (r *restClient) Agent(ctx context.Context, in *agenttypes.QueryAgent, _ ...grpc.CallOption) (*agenttypes.QueryAgentResponse, error) {
	out := &agenttypes.QueryAgentResponse{}
	return out, r.get(ctx, "/dydxprotocol/agent/agent/"+url.PathEscape(in.AgentId), out)
}

func (r *restClient) Params(ctx context.Context, _ *agenttypes.QueryParams, _ ...grpc.CallOption) (*agenttypes.QueryParamsResponse, error) {
	out := &agenttypes.QueryParamsResponse{}
	return out, r.get(ctx, "/dydxprotocol/agent/params", out)
}

// -- x/auth (chain.AccountClient) ----------------------------------------

func (r *restClient) Account(ctx context.Context, address string) (chain.AccountInfo, error) {
	out := &authtypes.QueryAccountResponse{}
	if err := r.get(ctx, "/cosmos/auth/v1beta1/accounts/"+url.PathEscape(address), out); err != nil {
		return chain.AccountInfo{}, fmt.Errorf("auth.Query/Account %s: %w", address, err)
	}
	var acc sdk.AccountI
	if err := r.registry.UnpackAny(out.Account, &acc); err != nil {
		return chain.AccountInfo{}, fmt.Errorf("unpack account %s: %w", address, err)
	}
	return chain.AccountInfo{AccountNumber: acc.GetAccountNumber(), Sequence: acc.GetSequence()}, nil
}

// -- tx service (chain.BroadcastClient) ----------------------------------

// BroadcastSync submits pre-signed tx bytes via POST /cosmos/tx/v1beta1/txs
// with BROADCAST_MODE_SYNC — CheckTx, not inclusion — matching what the gRPC
// client does, so Client.BroadcastSync treats both results the same way.
func (r *restClient) BroadcastSync(ctx context.Context, txBytes []byte) (chain.BroadcastResult, error) {
	reqBody, err := json.Marshal(map[string]string{
		"tx_bytes": base64.StdEncoding.EncodeToString(txBytes),
		"mode":     "BROADCAST_MODE_SYNC",
	})
	if err != nil {
		return chain.BroadcastResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/cosmos/tx/v1beta1/txs", bytes.NewReader(reqBody))
	if err != nil {
		return chain.BroadcastResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	out := &sdktx.BroadcastTxResponse{}
	if err := r.do(req, out); err != nil {
		return chain.BroadcastResult{}, err
	}
	if out.TxResponse == nil {
		return chain.BroadcastResult{}, fmt.Errorf("chain REST broadcast: empty tx_response")
	}
	return chain.BroadcastResult{
		TxHash: out.TxResponse.TxHash,
		Code:   out.TxResponse.Code,
		RawLog: out.TxResponse.RawLog,
	}, nil
}

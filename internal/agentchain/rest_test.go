package agentchain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/mcpcodec"
)

// newRESTClient serves handler and returns a Client pointed at it.
func newRESTClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := DialREST(srv.URL + "/") // a trailing slash must be tolerated
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func protoJSON(t *testing.T, w http.ResponseWriter, msg interface{ Reset() }) {
	t.Helper()
	body, err := mcpcodec.GetEncodingConfig().Codec.MarshalJSON(msg.(codecMessage))
	require.NoError(t, err)
	_, _ = w.Write(body)
}

// codecMessage is what codec.Codec.MarshalJSON accepts.
type codecMessage interface {
	Reset()
	String() string
	ProtoMessage()
}

func TestDialRESTRejectsABareHost(t *testing.T) {
	_, err := DialREST("127.0.0.1:1317")
	require.Error(t, err, "a URL without a scheme is a gRPC address, not a REST one")
}

func TestRESTAgentByIDHitsGatewayPath(t *testing.T) {
	var gotPath string
	c := newRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		protoJSON(t, w, &agenttypes.QueryAgentResponse{
			Agent: agenttypes.Agent{AgentId: "did:svp:abc", Endpoint: "https://x"},
		})
	})
	agent, found, err := c.AgentByID(context.Background(), "did:svp:abc")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "/dydxprotocol/agent/agent/did:svp:abc", gotPath)
	require.Equal(t, "https://x", agent.Endpoint)
}

// The gateway reports an unknown agent as HTTP 404 carrying the gRPC status
// in the body. That, and only that, means "not registered yet".
func TestRESTGatewayNotFoundIsNotRegistered(t *testing.T) {
	c := newRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": int(codes.NotFound), "message": "agent not found", "details": []any{},
		})
	})
	_, found, err := c.AgentByID(context.Background(), "did:svp:nobody")
	require.NoError(t, err)
	require.False(t, found)
}

// A bare 404 — a proxy that does not know the path, a wrong base URL — must
// surface as an error, not as "not registered", or a typo in the URL would
// register a second copy of an existing agent.
func TestRESTBare404IsAnError(t *testing.T) {
	c := newRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	_, _, err := c.AgentByID(context.Background(), "did:svp:abc")
	require.Error(t, err)
	require.NotEqual(t, codes.NotFound, status.Code(err))
}

func TestRESTOtherGatewayErrorsKeepTheirCode(t *testing.T) {
	c := newRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": int(codes.Internal), "message": "boom"})
	})
	_, _, err := c.AgentByID(context.Background(), "did:svp:abc")
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "boom")
}

func TestRESTParamsAndAccountRoundTrip(t *testing.T) {
	enc := mcpcodec.GetEncodingConfig()
	pub := secp256k1.GenPrivKey().PubKey()
	addr := sdk.AccAddress(pub.Address())
	acc := authtypes.NewBaseAccount(addr, pub, 7, 3)
	anyAcc, err := codectypes.NewAnyWithValue(acc)
	require.NoError(t, err)

	c := newRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dydxprotocol/agent/params":
			body, err := enc.Codec.MarshalJSON(&agenttypes.QueryParamsResponse{
				Params: agenttypes.Params{MinBond: sdk.NewInt64Coin("asvp", 5)},
			})
			require.NoError(t, err)
			_, _ = w.Write(body)
		case "/cosmos/auth/v1beta1/accounts/" + addr.String():
			body, err := enc.Codec.MarshalJSON(&authtypes.QueryAccountResponse{Account: anyAcc})
			require.NoError(t, err)
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	params, err := c.Params(context.Background())
	require.NoError(t, err)
	require.Equal(t, "5asvp", params.MinBond.String())

	info, err := c.Account(context.Background(), addr.String())
	require.NoError(t, err)
	require.Equal(t, uint64(7), info.AccountNumber)
	require.Equal(t, uint64(3), info.Sequence)
}

func TestRESTBroadcastSyncPostsSyncModeAndParsesResult(t *testing.T) {
	var gotBody map[string]string
	c := newRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/cosmos/tx/v1beta1/txs", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write([]byte(`{"tx_response":{"txhash":"ABC","code":0,"raw_log":""}}`))
	})
	res, err := c.BroadcastSync(context.Background(), []byte{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, "ABC", res.TxHash)
	require.Equal(t, "BROADCAST_MODE_SYNC", gotBody["mode"])
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte{1, 2, 3}), gotBody["tx_bytes"])
}

// A CheckTx failure comes back HTTP 200 with a non-zero code; Client turns it
// into an error the same way it does for gRPC.
func TestRESTBroadcastSyncCheckTxFailureIsAnError(t *testing.T) {
	c := newRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tx_response":{"txhash":"DEF","code":11,"raw_log":"out of gas"}}`))
	})
	_, err := c.BroadcastSync(context.Background(), []byte{1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of gas")
}

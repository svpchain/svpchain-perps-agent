// Command agent-register puts a running agent onto the chain, or brings an
// already-registered one back in line with what it now serves.
//
// Registration is not a deploy step and cannot be one. The value published on
// chain is the sha256 of the agent card as *served*, so the thing being
// registered has to be a running agent answering at a URL. This command
// fetches that card, hashes it, and signs a MsgRegisterAgent over the result.
//
// It signs locally, with the owner key. That is the part that changed when the
// agent went caller-signed: registration used to be SELF-registration, where
// the agent held an operator key on the remote and signed its own transaction,
// and this command was an A2A client that authenticated and asked it to. There
// is no key on the remote any more, so the transaction is built and signed
// here and the agent's only role is to serve the bytes that get hashed.
//
// One key fills both chain roles — owner and operator — so the agent id
// derives from the same key that pays the bond. See internal/owner for why,
// and for the constraint that follows: this key registers exactly one agent.
//
// It decides which call to make by asking the chain first:
//
//	unregistered                     → MsgRegisterAgent
//	registered, card hash moved      → MsgUpdateAgent
//	registered, endpoint moved       → MsgUpdateAgent
//	registered and current           → nothing, and it says so
//
// The owner key comes from SVPCHAIN_PERPS_AGENT_OWNER_KEY or -key-file, never
// from a flag: a key in argv is visible in `ps` and lands in shell history.
//
// The chain is reached over gRPC (-grpc) or over its Cosmos REST API (-rest,
// the gRPC-gateway, typically :1317) — exactly one. -rest and -chain-id also
// answer to -agent-chain-rest and -agent-chain-id, the flags scripts/deploy.sh
// takes, and default to SVPCHAIN_AGENT_CHAIN_REST and SVPCHAIN_AGENT_CHAIN_ID,
// the names its config file sets — so a sourced config file is enough:
//
//	. ~/.config/svpchain-perps-agent/config.sh && go run ./cmd/agent-register -url …
//
// Both do the same three
// things: read the current registration, read the signer's account, and
// broadcast. REST is there because the transaction is signed HERE, and a
// node's REST port is far more often reachable from an operator's machine
// than its gRPC port is.
//
// scripts/deploy.sh --register is the intended caller and passes the resolved
// endpoints. Running it by hand is useful when the agent has to be reached
// some other way — over an ssh tunnel before DNS is live, say:
//
//	SVPCHAIN_PERPS_AGENT_OWNER_KEY=… go run ./cmd/agent-register \
//	  -url http://127.0.0.1:8082 -chain-id svpchain-testnet-1 -grpc 127.0.0.1:9090
//	SVPCHAIN_PERPS_AGENT_OWNER_KEY=… go run ./cmd/agent-register \
//	  -url http://127.0.0.1:8082 -chain-id svpchain-testnet-1 -rest http://127.0.0.1:1317
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"

	"github.com/svpchain/svpchain-perps-agent/internal/agentchain"
	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/owner"
)

// Environment defaults for the chain flags — the names scripts/deploy.sh and
// its config file use, so the two agree without the script relaying them.
const (
	RestEnvVar    = "SVPCHAIN_AGENT_CHAIN_REST"
	ChainIDEnvVar = "SVPCHAIN_AGENT_CHAIN_ID"
)

// cardPath is the A2A well-known location; a2asrv serves the card there.
const cardPath = "/.well-known/agent-card.json"

type opts struct {
	url          string
	chainID      string
	grpcAddr     string
	restURL      string
	keyFile      string
	bond         string
	capabilities string
	metadata     string
	priceAmount  string
	priceUnit    string
	feeDenom     string
	feeAmount    string
	gasLimit     uint64
	dryRun       bool
	timeout      time.Duration
}

func main() {
	var o opts
	flag.StringVar(&o.url, "url", "", "base URL of the running agent; registered as its endpoint and where the card is fetched")
	flag.StringVar(&o.chainID, "chain-id", os.Getenv(ChainIDEnvVar), "chain id of the chain carrying x/agent (default $"+ChainIDEnvVar+")")
	flag.StringVar(&o.chainID, "agent-chain-id", os.Getenv(ChainIDEnvVar), "alias of -chain-id, the name scripts/deploy.sh uses")
	flag.StringVar(&o.grpcAddr, "grpc", "", "gRPC address of that chain (host:port); exactly one of -grpc and -rest")
	flag.StringVar(&o.restURL, "rest", os.Getenv(RestEnvVar), "Cosmos REST base URL of that chain (scheme://host:port, the gRPC-gateway, typically :1317); exactly one of -grpc and -rest (default $"+RestEnvVar+")")
	flag.StringVar(&o.restURL, "agent-chain-rest", os.Getenv(RestEnvVar), "alias of -rest, the name scripts/deploy.sh uses")
	flag.StringVar(&o.keyFile, "key-file", "", "owner key file, when "+owner.KeyEnvVar+" is not set")
	flag.StringVar(&o.bond, "bond", "", "initial bond as a coin, e.g. 5000000000000000000000asvp; empty takes the module's MinBond")
	flag.StringVar(&o.capabilities, "capabilities", "", "comma-separated capability tags for discovery; at least one is required")
	flag.StringVar(&o.metadata, "metadata", "", "opaque owner metadata; empty leaves an existing value alone")
	flag.StringVar(&o.priceAmount, "price-amount", "", "fee per -price-unit as a positive integer in the settlement token's smallest unit; required to register, empty on an update leaves the registered pricing alone")
	flag.StringVar(&o.priceUnit, "price-unit", "call", "unit the price is quoted against")
	flag.StringVar(&o.feeDenom, "fee-denom", config.DefaultFeeDenom, "fee denom")
	flag.StringVar(&o.feeAmount, "fee-amount", config.DefaultFeeAmount, "fee amount")
	flag.Uint64Var(&o.gasLimit, "gas-limit", config.DefaultFeeGasLimit, "gas limit")
	flag.BoolVar(&o.dryRun, "dry-run", false, "print what would be submitted and exit without broadcasting")
	flag.DurationVar(&o.timeout, "timeout", 90*time.Second, "deadline for the whole exchange")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()

	if err := run(ctx, o, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "agent-register: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o opts, w io.Writer) error {
	baseURL := strings.TrimSuffix(strings.TrimSpace(o.url), "/")
	if baseURL == "" {
		return fmt.Errorf("-url is required: the agent has to be running and reachable to register itself")
	}
	if o.chainID == "" {
		return fmt.Errorf("-chain-id is required: it names the chain carrying x/agent, and the signature commits to it")
	}
	if (o.grpcAddr == "") == (o.restURL == "") {
		return fmt.Errorf("exactly one of -grpc and -rest (or $%s) is required: how to reach the chain carrying x/agent", RestEnvVar)
	}
	tags := splitTags(o.capabilities)
	if len(tags) == 0 {
		return fmt.Errorf("-capabilities is required: an agent advertising none appears in no capability index and the chain refuses it")
	}

	priv, addrStr, err := owner.Load(o.keyFile)
	if err != nil {
		return err
	}
	if priv == nil {
		return fmt.Errorf("no owner key in %s or -key-file — registration is the owner "+
			"proving it holds the key this agent is registered under (see --gen-owner-key)",
			owner.KeyEnvVar)
	}
	ownerAddr, err := sdk.AccAddressFromBech32(addrStr)
	if err != nil {
		return fmt.Errorf("derive owner address: %w", err)
	}

	// What a verifier does later, done here first: fetch the card and hash the
	// exact bytes served. A proxy rewriting the body, or a stale process behind
	// the URL, is caught before it becomes an on-chain claim nobody can verify.
	cardBytes, err := fetchCard(ctx, baseURL)
	if err != nil {
		return err
	}
	cardHash := sha256.Sum256(cardBytes)
	if err := checkCardURL(cardBytes, baseURL); err != nil {
		return err
	}

	want := agentchain.Desired{
		Endpoint:       baseURL,
		CapabilityHash: cardHash[:],
		Capabilities:   tags,
		Metadata:       o.metadata,
	}
	if o.priceAmount != "" {
		want.Pricing = &agenttypes.Pricing{Amount: o.priceAmount, Unit: o.priceUnit}
		if err := agenttypes.ValidatePricing(want.Pricing); err != nil {
			return fmt.Errorf("-price-amount/-price-unit: %w", err)
		}
	}

	client, err := dial(ctx, o)
	if err != nil {
		return err
	}
	defer client.Close()

	agentID := agentchain.AgentID(ownerAddr)
	existing, found, err := client.AgentByID(ctx, agentID)
	if err != nil {
		return err
	}

	var msg sdk.Msg
	var action string
	var bond sdk.Coin
	switch {
	case !found:
		if want.Pricing == nil {
			return fmt.Errorf("-price-amount is required to register: the chain refuses an agent that advertises no price")
		}
		bond, err = resolveBond(ctx, client, o.bond)
		if err != nil {
			return err
		}
		msg = agentchain.BuildRegister(ownerAddr, want, bond)
		action = "register"
		fmt.Fprintf(w, "not registered — registering %s\n", agentID)
		fmt.Fprintf(w, "  bond     %s\n", bond)
	default:
		drift := agentchain.Drift(existing, want)
		if len(drift) == 0 {
			fmt.Fprintf(w, "already registered and current — nothing to do\n")
			fmt.Fprintf(w, "  agent    %s\n", existing.AgentId)
			fmt.Fprintf(w, "  status   %s\n", existing.Status)
			fmt.Fprintf(w, "  bond     %s\n", existing.Bond)
			fmt.Fprintf(w, "  endpoint %s\n", existing.Endpoint)
			return nil
		}
		msg = agentchain.BuildUpdate(existing, want)
		action = "update"
		fmt.Fprintf(w, "registered but stale — updating %s\n", agentID)
		for _, d := range drift {
			fmt.Fprintf(w, "  · %s\n", d)
		}
	}

	fmt.Fprintf(w, "  owner    %s (also the operator)\n", addrStr)
	fmt.Fprintf(w, "  endpoint %s\n", baseURL)
	fmt.Fprintf(w, "  card     sha256 %x\n", cardHash)
	fmt.Fprintf(w, "  tags     %s\n", strings.Join(tags, ","))

	// ValidateBasic here rather than at the mempool: the same checks run
	// chain-side, and failing locally names the field instead of returning a
	// broadcast error code.
	if v, ok := msg.(interface{ ValidateBasic() error }); ok {
		if err := v.ValidateBasic(); err != nil {
			return fmt.Errorf("%s message is invalid: %w", action, err)
		}
	}
	if o.dryRun {
		fmt.Fprintf(w, "dry-run — not broadcasting\n")
		return nil
	}

	acct, err := client.Account(ctx, addrStr)
	if err != nil {
		// An account the chain has never seen is the ordinary shape of "the key
		// was minted but never funded", and it is worth more than the raw
		// NotFound: say what has to land there, in the denom it has to land in.
		return fmt.Errorf("%w\n%s", err, fundingHint(ctx, client, action, bond, o))
	}
	txBytes, err := owner.SignTx(priv, o.chainID, acct, []sdk.Msg{msg}, owner.FeeSpec{
		Denom:    o.feeDenom,
		Amount:   o.feeAmount,
		GasLimit: o.gasLimit,
	})
	if err != nil {
		return err
	}
	res, err := client.BroadcastSync(ctx, txBytes)
	if err != nil {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	fmt.Fprintf(w, "%s submitted — tx %s\n", action, res.TxHash)
	return nil
}

// dial picks the transport the flags named. Everything after this point is
// transport-blind: the same reads, the same signature, the same broadcast.
func dial(ctx context.Context, o opts) (*agentchain.Client, error) {
	if o.restURL != "" {
		return agentchain.DialREST(o.restURL)
	}
	return agentchain.Dial(ctx, o.grpcAddr)
}

// resolveBond takes the operator's coin, or asks the module for its minimum.
// Registering below MinBond is rejected by the handler, so defaulting to it is
// the smallest amount that can succeed rather than an arbitrary pick.
func resolveBond(ctx context.Context, c *agentchain.Client, spec string) (sdk.Coin, error) {
	if spec != "" {
		coin, err := sdk.ParseCoinNormalized(spec)
		if err != nil {
			return sdk.Coin{}, fmt.Errorf("-bond %q: %w", spec, err)
		}
		return coin, nil
	}
	params, err := c.Params(ctx)
	if err != nil {
		return sdk.Coin{}, fmt.Errorf("%w — pass -bond to skip the lookup", err)
	}
	return params.MinBond, nil
}

// fundingHint spells out what the owner account still needs. Registering pays
// a module registration fee AND posts the bond on top of gas; an update pays
// only gas, so quoting the bond there would send an operator hunting for funds
// they do not need.
func fundingHint(ctx context.Context, c *agentchain.Client, action string, bond sdk.Coin, o opts) string {
	gas := sdk.Coin{Denom: o.feeDenom, Amount: sdkmath.NewInt(0)}
	if amt, ok := sdkmath.NewIntFromString(o.feeAmount); ok {
		gas.Amount = amt
	}
	if action != "register" {
		return fmt.Sprintf("  it needs gas: %s", gas)
	}

	var parts []string
	total := gas
	params, err := c.Params(ctx)
	if err == nil {
		parts = append(parts, fmt.Sprintf("registration fee %s", params.RegistrationFee))
		if params.RegistrationFee.Denom == total.Denom {
			total = total.Add(params.RegistrationFee)
		}
	}
	parts = append(parts, fmt.Sprintf("bond %s", bond))
	if bond.Denom == total.Denom {
		total = total.Add(bond)
	}
	parts = append(parts, fmt.Sprintf("gas %s", gas))
	return fmt.Sprintf("  it needs %s\n  total at least %s", strings.Join(parts, " + "), total)
}

func fetchCard(ctx context.Context, baseURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+cardPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build card request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s%s: %w — the agent has to be running and reachable", baseURL, cardPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s%s: HTTP %d", baseURL, cardPath, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read card: %w", err)
	}
	return body, nil
}

// checkCardURL catches the endpoint that a card does not agree with.
//
// The agent builds its interface URL from the public_url it was configured
// with, so a card advertising some other host means -url and the deployed
// config disagree — and registering -url would publish an endpoint whose own
// card points callers somewhere else.
func checkCardURL(cardBytes []byte, baseURL string) error {
	var card struct {
		SupportedInterfaces []struct {
			URL string `json:"url"`
		} `json:"supportedInterfaces"`
	}
	if err := json.Unmarshal(cardBytes, &card); err != nil {
		return fmt.Errorf("parse agent card: %w", err)
	}
	if len(card.SupportedInterfaces) == 0 {
		return nil
	}
	if got := card.SupportedInterfaces[0].URL; !strings.HasPrefix(got, baseURL+"/") {
		return fmt.Errorf("the card served at %s advertises its interface at %s — "+
			"the agent's public_url and -url disagree, so registering this endpoint "+
			"would point callers at a card that sends them elsewhere", baseURL, got)
	}
	return nil
}

func splitTags(csv string) []string {
	var out []string
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

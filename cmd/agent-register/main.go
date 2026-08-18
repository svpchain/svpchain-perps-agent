// Command agent-register puts a running agent onto the chain, or brings an
// already-registered one back in line with what it now serves.
//
// Registration is not a deploy step and cannot be one. The value published on
// chain is the sha256 of the agent card as *served*, so the thing that
// registers has to be a running agent answering at a URL — which is why
// agent_self_register is a tool on the A2A surface rather than a subcommand of
// the binary. This command is a client: it authenticates as the operator over
// the ordinary auth_challenge / auth_verify flow and calls that tool.
//
// It decides which call to make by asking first:
//
//	unregistered                     → agent_self_register
//	registered, card hash moved      → agent_self_update
//	registered, endpoint moved       → agent_self_update
//	registered and current           → nothing, and it says so
//
// Both of those drifts are silent failures otherwise. A stale capability hash
// makes verifiers read the agent as unverified while every process is healthy,
// and a stale endpoint points them at a URL that may no longer answer.
//
// Before any of that it fetches /.well-known/agent-card.json and checks its
// sha256 against the hash the agent says it would publish. That is exactly what
// a verifier does later, so a mismatch here — a proxy rewriting the body, a
// stale process behind the URL — is caught before it becomes an on-chain claim
// nobody can verify.
//
// The operator key comes from SVPCHAIN_PERPS_AGENT_OPERATOR_KEY, the same
// variable the agent itself reads, and never from a flag: a key in argv is
// visible in `ps` and lands in shell history. It signs the auth challenge only;
// the registration transaction is signed by the agent, with its own copy.
//
// scripts/deploy.sh --register is the intended caller and passes the public
// URL. Running it by hand is useful when the agent has to be reached some other
// way — over an ssh tunnel before DNS is live, say:
//
//	SVPCHAIN_PERPS_AGENT_OPERATOR_KEY=… go run ./cmd/agent-register -url http://127.0.0.1:8082
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/svpchain/svpchain-perps-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-perps-agent/internal/agentchain"
	"github.com/svpchain/svpchain-perps-agent/internal/config"
	"github.com/svpchain/svpchain-perps-agent/internal/delegated"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"
	"github.com/svpchain/svpchain-perps-agent/internal/operator"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

func main() {
	url := flag.String("url", "", "base URL of the running agent (its /invoke and card are served under this)")
	bond := flag.String("bond", "", "initial bond as a coin, e.g. 1000000usvp; empty takes the module's MinBond")
	timeout := flag.Duration("timeout", 90*time.Second, "deadline for the whole exchange")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := run(ctx, *url, *bond, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "agent-register: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, baseURL, bond string, w io.Writer) error {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("-url is required: the agent has to be running and reachable to register itself")
	}

	// Config.Operator zero-valued: Load then reads only the environment
	// variable, which is how the deploy hands the key over — a config file
	// path here would be this machine's, not the remote's.
	priv, addr, err := operator.Load(config.Operator{})
	if err != nil {
		return err
	}
	if priv == nil {
		return fmt.Errorf("no operator key in %s — only the operator can register this agent, "+
			"and the key proves it (see --gen-operator-key)", operator.KeyEnvVar)
	}

	served, cardHash, err := fetchCard(ctx, baseURL)
	if err != nil {
		return err
	}

	client, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{
		a2a.NewAgentInterface(baseURL+"/invoke", a2a.TransportProtocolJSONRPC),
	})
	if err != nil {
		return fmt.Errorf("connect to the agent at %s: %w", baseURL, err)
	}
	defer client.Destroy()
	agent := &agentClient{client: client}

	// agent_identity needs no credential; it is the one execution tool that
	// answers before there is anything to authorize.
	var id delegated.IdentityOutput
	if err := agent.call(ctx, toolbridge.SkillExecution, "agent_identity", struct{}{}, &id); err != nil {
		return err
	}
	if id.Operator != addr {
		return fmt.Errorf("the agent at %s runs under operator %s, but %s derives %s — "+
			"that is a different agent, and registering it with this key is not possible",
			baseURL, id.Operator, operator.KeyEnvVar, addr)
	}
	// What the agent says it would publish must be what it is actually
	// serving, or the registration is a claim about bytes nobody can fetch.
	if id.CardHash != "" && id.CardHash != cardHash {
		return fmt.Errorf("the card served at %s hashes to %s, but the agent would publish %s — "+
			"something between here and the agent is rewriting the card, or the URL reaches a different process",
			baseURL, cardHash, id.CardHash)
	}

	tool, why := decide(id, served)
	if tool == "" {
		fmt.Fprintf(w, "already registered and current — nothing to do\n")
		fmt.Fprintf(w, "  agent    %s\n", id.AgentID)
		fmt.Fprintf(w, "  status   %s\n", id.Status)
		fmt.Fprintf(w, "  bond     %s\n", id.Bond)
		fmt.Fprintf(w, "  endpoint %s\n", id.Endpoint)
		return nil
	}
	fmt.Fprintf(w, "%s: %s\n", tool, why)

	if err := agent.authenticate(ctx, priv, addr); err != nil {
		return err
	}

	args, err := registerArgs(tool, bond, w)
	if err != nil {
		return err
	}
	var res delegated.ExecResult
	if err := agent.call(ctx, toolbridge.SkillExecution, tool, args, &res); err != nil {
		return err
	}
	// A non-zero code is a chain-side rejection that still came back as a
	// successful call, so it has to be checked rather than assumed away.
	if res.Code != 0 {
		return fmt.Errorf("the chain rejected the transaction (code %d): %s", res.Code, res.RawLog)
	}

	fmt.Fprintf(w, "  agent    %s\n", res.AgentID)
	fmt.Fprintf(w, "  operator %s\n", res.Principal)
	fmt.Fprintf(w, "  tx       %s\n", res.TxHash)
	// BroadcastSync returns once the transaction passes CheckTx, which is not
	// inclusion in a block. Saying so beats implying a confirmation this
	// cannot have.
	fmt.Fprintf(w, "\nBroadcast (CheckTx passed). Confirm it landed with agent_identity:\n")
	fmt.Fprintf(w, "  registered should be true and card_hash_matches true.\n")
	return nil
}

// decide picks the operation this deployment needs, and the reason to print.
// An empty tool means the registry already matches what is being served.
//
// The two drift cases are separate because the capability hash does not cover
// the endpoint: a card that never changed can still be registered against a URL
// that has, which is exactly what renaming the advertised host does.
func decide(id delegated.IdentityOutput, served string) (tool, why string) {
	switch {
	case !id.Registered:
		return "agent_self_register", "this agent is not on chain yet"
	case !id.CardHashMatches:
		return "agent_self_update", fmt.Sprintf(
			"the served card no longer matches the registration (registered %s, serving %s)",
			short(id.RegisteredCapabilityHash), short(id.CardHash))
	case served != "" && id.Endpoint != served:
		return "agent_self_update", fmt.Sprintf(
			"the registered endpoint is %s but this agent advertises %s", id.Endpoint, served)
	default:
		return "", ""
	}
}

// registerArgs builds the tool's input. Only registration takes a bond —
// agent_self_update never touches it, so a bond passed alongside one is a
// misunderstanding worth naming rather than silently dropping.
func registerArgs(tool, bond string, w io.Writer) (any, error) {
	if tool != "agent_self_register" {
		if bond != "" {
			fmt.Fprintf(w, "  (-bond ignored: %s does not change the bond; use build_deposit_bond)\n", tool)
		}
		return agentchain.EmptyInput{}, nil
	}
	in := delegated.SelfRegisterInput{}
	if bond != "" {
		coin, err := sdk.ParseCoinNormalized(bond)
		if err != nil {
			return nil, fmt.Errorf("-bond %q is not a coin (want e.g. 1000000usvp): %w", bond, err)
		}
		in.Bond = &agentchain.Coin{Denom: coin.Denom, Amount: coin.Amount.String()}
	}
	return in, nil
}

// fetchCard returns the base URL the agent advertises in its card and the
// sha256 of the card bytes as served. Both come from one fetch on purpose: they
// are the two things a verifier reads, and reading them any other way would be
// checking something other than what gets verified.
func fetchCard(ctx context.Context, baseURL string) (endpoint, hash string, err error) {
	const path = "/.well-known/agent-card.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch %s%s: %w (is the agent running and routed?)", baseURL, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch %s%s: HTTP %d — the card must be reachable at the URL "+
			"being registered, because that is where verifiers fetch it", baseURL, path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read agent card: %w", err)
	}
	sum := sha256.Sum256(body)

	var card a2a.AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		return "", "", fmt.Errorf("parse agent card: %w", err)
	}
	// The card advertises "<public_url>/invoke"; the registry carries the base.
	if len(card.SupportedInterfaces) > 0 {
		endpoint = strings.TrimSuffix(card.SupportedInterfaces[0].URL, "/invoke")
	}
	return endpoint, hex.EncodeToString(sum[:]), nil
}

// agentClient speaks this agent's A2A envelope: one JSON object naming a skill,
// a tool and its arguments, carried as the message text.
type agentClient struct {
	client *a2aclient.Client
	bearer string
}

// authenticate runs the same challenge/signature exchange any caller does, with
// the operator's own key — which is what makes the call authorized: the
// registry operations refuse anyone whose authenticated owner is not the
// operator this agent runs as.
func (a *agentClient) authenticate(ctx context.Context, priv interface{ Sign([]byte) ([]byte, error) }, owner string) error {
	var challenge tools.AuthChallengeOutput
	if err := a.call(ctx, toolbridge.SkillAuth, "auth_challenge",
		tools.AuthChallengeInput{Owner: owner}, &challenge); err != nil {
		return err
	}
	// The agent rebuilds the challenge text from its own stored state and
	// verifies against that, so the bytes signed here must be the ones it
	// handed back, untouched.
	sig, err := priv.Sign([]byte(challenge.Challenge))
	if err != nil {
		return fmt.Errorf("sign the auth challenge: %w", err)
	}
	var verified tools.AuthVerifyOutput
	if err := a.call(ctx, toolbridge.SkillAuth, "auth_verify", tools.AuthVerifyInput{
		Nonce:     challenge.Nonce,
		Signature: base64.StdEncoding.EncodeToString(sig),
	}, &verified); err != nil {
		return err
	}
	a.bearer = verified.BearerToken
	return nil
}

// call sends one operation and decodes its result into out (nil to discard).
//
// The envelope carries the bearer rather than an Authorization header: the
// field exists for exactly this case, and it keeps the credential on the same
// object as the request instead of threading transport options through.
func (a *agentClient) call(ctx context.Context, skill, tool string, args, out any) error {
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("encode %s arguments: %w", tool, err)
	}
	body, err := json.Marshal(a2aserver.Request{
		Skill:  skill,
		Tool:   tool,
		Args:   rawArgs,
		Bearer: a.bearer,
	})
	if err != nil {
		return fmt.Errorf("encode %s request: %w", tool, err)
	}

	result, err := a.client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(string(body))),
	})
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	text, err := resultText(result)
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}

	var resp a2aserver.Response
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		// A request the executor could not even dispatch comes back as plain
		// text rather than a response envelope; surfacing it verbatim beats a
		// decode error about JSON the caller never wrote.
		return fmt.Errorf("%s: %s", tool, strings.TrimSpace(text))
	}
	if !resp.OK {
		return fmt.Errorf("%s refused: %s", tool, resp.Error)
	}
	if out == nil {
		return nil
	}
	// Response.Result is decoded as `any`; a re-marshal is the shortest honest
	// way back to the typed shape the tool actually returned.
	encoded, err := json.Marshal(resp.Result)
	if err != nil {
		return fmt.Errorf("re-encode %s result: %w", tool, err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("decode %s result: %w", tool, err)
	}
	return nil
}

// resultText digs the agent's answer out of the send result. The executor
// completes the task and hangs its answer on the terminal status update, so the
// reply arrives as the task's status message rather than a bare message.
func resultText(result a2a.SendMessageResult) (string, error) {
	var msg *a2a.Message
	switch v := result.(type) {
	case *a2a.Task:
		if v.Status.Message == nil {
			return "", fmt.Errorf("task %s ended in state %q with no answer", v.ID, v.Status.State)
		}
		msg = v.Status.Message
	case *a2a.Message:
		msg = v
	default:
		return "", fmt.Errorf("unexpected result type %T", result)
	}
	var out strings.Builder
	for _, part := range msg.Parts {
		if part == nil {
			continue
		}
		out.WriteString(part.Text())
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", fmt.Errorf("empty answer")
	}
	return text, nil
}

// short abbreviates a hex hash for a one-line reason.
func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12] + "…"
}

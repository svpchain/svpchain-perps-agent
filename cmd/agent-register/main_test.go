package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-perps-agent/internal/delegated"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/auth"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"
	"github.com/svpchain/svpchain-perps-agent/internal/operator"
)

// The agent under test is stood up out of the same a2asrv pieces the real
// server uses — the JSON-RPC handler and the static card handler — with a fake
// executor in place of the operation registry. That is the point: it pins the
// wire format this client speaks against the one the agent actually serves,
// including the event sequence the answer rides on, without needing a chain.
type fakeAgent struct {
	t *testing.T

	// respond answers one dispatched request. Set per test.
	respond func(req a2aserver.Request) (any, error)

	calls   []a2aserver.Request
	nonce   string
	chainID string
}

func (f *fakeAgent) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		var text strings.Builder
		for _, p := range execCtx.Message.Parts {
			text.WriteString(p.Text())
		}
		var req a2aserver.Request
		if err := json.Unmarshal([]byte(text.String()), &req); err != nil {
			f.t.Errorf("agent received something other than a request envelope: %s", text.String())
		}
		f.calls = append(f.calls, req)

		resp := a2aserver.Response{Skill: req.Skill, Tool: req.Tool}
		if result, err := f.respond(req); err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK, resp.Result = true, result
		}
		body, err := json.Marshal(resp)
		if err != nil {
			f.t.Fatal(err)
		}
		reply := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewTextPart(string(body)))
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, reply), nil)
	}
}

func (f *fakeAgent) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

func (f *fakeAgent) toolNames() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.Tool)
	}
	return out
}

// startAgent serves a card and an /invoke endpoint, and returns the base URL
// plus the sha256 of the card exactly as served — the value a verifier would
// recompute, and therefore what the identity fixture has to claim.
func startAgent(t *testing.T, f *fakeAgent) (baseURL, cardHash string) {
	t.Helper()
	var cardBytes []byte
	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(f)))
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cardBytes)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	card := &a2a.AgentCard{
		Name:        "svpchain-perps-agent",
		Description: "test",
		Version:     "0.0.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(srv.URL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
	}
	b, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	cardBytes = b
	sum := sha256.Sum256(b)
	return srv.URL, hex.EncodeToString(sum[:])
}

// newOperator mints a key, exports it the way the deploy hands it over, and
// returns the address it derives.
func newOperator(t *testing.T) string {
	t.Helper()
	key, addr, err := operator.Generate()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(operator.KeyEnvVar, key)
	return addr
}

// authAndRegistry answers the auth handshake for real — it verifies the
// signature the client produced recovers to the operator address, which is what
// the agent's auth_verify does — and hands execution calls to next.
func (f *fakeAgent) authAndRegistry(owner string, next func(req a2aserver.Request) (any, error)) func(a2aserver.Request) (any, error) {
	f.nonce, f.chainID = "abc123", "svp-test-1"
	expires := time.Now().Add(5 * time.Minute)
	challenge := auth.BuildChallenge(f.chainID, f.nonce, expires)

	return func(req a2aserver.Request) (any, error) {
		switch req.Tool {
		case "auth_challenge":
			var in tools.AuthChallengeInput
			if err := json.Unmarshal(req.Args, &in); err != nil {
				return nil, err
			}
			if in.Owner != owner {
				f.t.Errorf("auth_challenge owner = %q, want the operator address %q", in.Owner, owner)
			}
			return tools.AuthChallengeOutput{
				Challenge: challenge,
				Nonce:     f.nonce,
				ExpiresAt: expires.Unix(),
			}, nil
		case "auth_verify":
			var in tools.AuthVerifyInput
			if err := json.Unmarshal(req.Args, &in); err != nil {
				return nil, err
			}
			if in.Nonce != f.nonce {
				f.t.Errorf("auth_verify nonce = %q, want %q", in.Nonce, f.nonce)
			}
			sig, err := base64.StdEncoding.DecodeString(in.Signature)
			if err != nil {
				f.t.Fatalf("signature is not base64: %v", err)
			}
			// The real server rebuilds the challenge from its own state and
			// recovers the signer from it; if the client signed anything else,
			// or signed it differently, this is where it shows.
			recovered, err := auth.RecoverOwner(challenge, sig)
			if err != nil {
				f.t.Fatalf("recover the client's signature: %v", err)
			}
			if recovered != owner {
				f.t.Errorf("signature recovers to %q, want the operator %q", recovered, owner)
			}
			return tools.AuthVerifyOutput{BearerToken: "bearer-token", Owner: owner, ExpiresAt: expires.Unix()}, nil
		default:
			return next(req)
		}
	}
}

func TestRegistersAnAgentThatIsNotOnChain(t *testing.T) {
	f := &fakeAgent{t: t}
	baseURL, cardHash := startAgent(t, f)
	owner := newOperator(t)

	f.respond = f.authAndRegistry(owner, func(req a2aserver.Request) (any, error) {
		switch req.Tool {
		case "agent_identity":
			return delegated.IdentityOutput{Operator: owner, AgentID: "did:svp:" + owner, CardHash: cardHash}, nil
		case "agent_self_register":
			if req.Bearer != "bearer-token" {
				t.Errorf("agent_self_register carried bearer %q, want the one auth_verify minted", req.Bearer)
			}
			return delegated.ExecResult{TxHash: "ABC123", AgentID: "did:svp:" + owner, Principal: owner}, nil
		}
		t.Errorf("unexpected tool %q", req.Tool)
		return nil, nil
	})

	var out bytes.Buffer
	if err := run(t.Context(), baseURL, "", &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	want := []string{"agent_identity", "auth_challenge", "auth_verify", "agent_self_register"}
	if got := strings.Join(f.toolNames(), ","); got != strings.Join(want, ",") {
		t.Errorf("call sequence = %s, want %s", got, strings.Join(want, ","))
	}
	if !strings.Contains(out.String(), "ABC123") {
		t.Errorf("the tx hash is not in the output:\n%s", out.String())
	}
}

// The bond is optional and defaults to the module's MinBond; when given it has
// to reach the tool as a coin, not as the string the operator typed.
func TestRegisterPassesTheBondAsACoin(t *testing.T) {
	f := &fakeAgent{t: t}
	baseURL, cardHash := startAgent(t, f)
	owner := newOperator(t)

	var got delegated.SelfRegisterInput
	f.respond = f.authAndRegistry(owner, func(req a2aserver.Request) (any, error) {
		switch req.Tool {
		case "agent_identity":
			return delegated.IdentityOutput{Operator: owner, CardHash: cardHash}, nil
		case "agent_self_register":
			if err := json.Unmarshal(req.Args, &got); err != nil {
				return nil, err
			}
			return delegated.ExecResult{TxHash: "OK"}, nil
		}
		return nil, nil
	})

	var out bytes.Buffer
	if err := run(t.Context(), baseURL, "1500000usvp", &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if got.Bond == nil {
		t.Fatal("the bond never reached agent_self_register")
	}
	if got.Bond.Denom != "usvp" || got.Bond.Amount != "1500000" {
		t.Errorf("bond = %+v, want 1500000 usvp", *got.Bond)
	}
}

// The two drifts that make a healthy agent read as unverified, and the case
// where there is nothing to do. Each is decided from what the agent reports
// about itself, before any transaction is signed.
func TestChoosesUpdateOverRegisterWhenTheRegistrationHasDrifted(t *testing.T) {
	cases := map[string]struct {
		identity func(baseURL, cardHash, owner string) delegated.IdentityOutput
		want     string
		reason   string
	}{
		"card hash moved": {
			identity: func(baseURL, cardHash, owner string) delegated.IdentityOutput {
				return delegated.IdentityOutput{
					Operator: owner, Registered: true, Endpoint: baseURL,
					CardHash: cardHash, RegisteredCapabilityHash: strings.Repeat("ab", 32),
					CardHashMatches: false,
				}
			},
			want:   "agent_self_update",
			reason: "no longer matches",
		},
		"endpoint moved": {
			identity: func(baseURL, cardHash, owner string) delegated.IdentityOutput {
				return delegated.IdentityOutput{
					Operator: owner, Registered: true, Endpoint: "https://old.example.org/perps",
					CardHash: cardHash, RegisteredCapabilityHash: cardHash, CardHashMatches: true,
				}
			},
			want:   "agent_self_update",
			reason: "registered endpoint",
		},
		"already current": {
			identity: func(baseURL, cardHash, owner string) delegated.IdentityOutput {
				return delegated.IdentityOutput{
					Operator: owner, Registered: true, Endpoint: baseURL, Status: "ACTIVE",
					CardHash: cardHash, RegisteredCapabilityHash: cardHash, CardHashMatches: true,
				}
			},
			want:   "",
			reason: "nothing to do",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeAgent{t: t}
			baseURL, cardHash := startAgent(t, f)
			owner := newOperator(t)

			f.respond = f.authAndRegistry(owner, func(req a2aserver.Request) (any, error) {
				switch req.Tool {
				case "agent_identity":
					return tc.identity(baseURL, cardHash, owner), nil
				case "agent_self_update":
					return delegated.ExecResult{TxHash: "UPDATED"}, nil
				}
				t.Errorf("unexpected tool %q", req.Tool)
				return nil, nil
			})

			var out bytes.Buffer
			if err := run(t.Context(), baseURL, "", &out); err != nil {
				t.Fatalf("run: %v\n%s", err, out.String())
			}
			called := strings.Join(f.toolNames(), ",")
			if tc.want == "" {
				// Nothing to do must mean exactly that: no handshake, no
				// transaction, no gas spent restating what is already true.
				if called != "agent_identity" {
					t.Errorf("a current agent still called %s", called)
				}
			} else if !strings.Contains(called, tc.want) {
				t.Errorf("call sequence = %s, want it to include %s", called, tc.want)
			}
			if !strings.Contains(out.String(), tc.reason) {
				t.Errorf("output does not explain the decision (%q):\n%s", tc.reason, out.String())
			}
		})
	}
}

// The pre-flight check: what the agent says it would publish has to be the
// sha256 of what it is actually serving. A mismatch means the registration
// would claim a hash for bytes nobody can fetch, and it must not proceed to
// signing anything.
func TestRefusesWhenTheServedCardDoesNotHashToWhatTheAgentWouldPublish(t *testing.T) {
	f := &fakeAgent{t: t}
	baseURL, _ := startAgent(t, f)
	owner := newOperator(t)

	f.respond = f.authAndRegistry(owner, func(req a2aserver.Request) (any, error) {
		if req.Tool == "agent_identity" {
			return delegated.IdentityOutput{Operator: owner, CardHash: strings.Repeat("cd", 32)}, nil
		}
		t.Errorf("reached %q despite the card mismatch", req.Tool)
		return nil, nil
	})

	var out bytes.Buffer
	err := run(t.Context(), baseURL, "", &out)
	if err == nil {
		t.Fatal("run proceeded with a card that does not match what would be published")
	}
	if !strings.Contains(err.Error(), "rewriting the card") {
		t.Errorf("error does not explain the mismatch: %v", err)
	}
}

// Registering with a key that is not this agent's operator cannot work — the
// agent would refuse — but saying so before the handshake makes the mistake
// legible rather than surfacing it as an authorization failure.
func TestRefusesWhenTheKeyIsNotThisAgentsOperator(t *testing.T) {
	f := &fakeAgent{t: t}
	baseURL, cardHash := startAgent(t, f)
	newOperator(t)

	other := "svp1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	f.respond = f.authAndRegistry(other, func(req a2aserver.Request) (any, error) {
		if req.Tool == "agent_identity" {
			return delegated.IdentityOutput{Operator: other, CardHash: cardHash}, nil
		}
		t.Errorf("reached %q despite the operator mismatch", req.Tool)
		return nil, nil
	})

	var out bytes.Buffer
	err := run(t.Context(), baseURL, "", &out)
	if err == nil {
		t.Fatal("run proceeded against an agent it does not hold the operator key for")
	}
	if !strings.Contains(err.Error(), "different agent") {
		t.Errorf("error does not explain the mismatch: %v", err)
	}
}

// Keyless is a supported way to run the agent, so it is a supported way to
// arrive here — with a message about what is missing rather than a panic or an
// authorization error from the far end.
func TestRefusesWithoutAnOperatorKey(t *testing.T) {
	t.Setenv(operator.KeyEnvVar, "")
	var out bytes.Buffer
	err := run(t.Context(), "http://127.0.0.1:1", "", &out)
	if err == nil || !strings.Contains(err.Error(), operator.KeyEnvVar) {
		t.Fatalf("want an error naming %s, got %v", operator.KeyEnvVar, err)
	}
}

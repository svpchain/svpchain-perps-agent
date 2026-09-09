package a2aserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// newAuthedStack builds the executor in the shape a deployment runs: the whole
// proxied surface plus the resolver that carries a caller's bearer onto it.
//
// ★ It used to build a great deal more — a tenant store, a nonce store, an IP
// limiter, a policy engine — because auth_challenge and auth_verify ran in
// this process and minted into local state. They are two more proxied tools
// now, so the whole flow lives on the MCP server and there is no local state
// left to construct or to test. What is left to test here is that a caller's
// bearer reaches the operation, which is this package's actual job.
func newAuthedStack(t *testing.T) (*Executor, *toolbridge.Registry) {
	t.Helper()
	reg := toolbridge.NewRemote(nil)
	return NewFullExecutor(reg, &AuthResolver{}), reg
}

// dispatch runs one envelope through the executor and decodes the Response.
func dispatch(t *testing.T, e *Executor, ec *a2asrv.ExecutorContext) Response {
	t.Helper()
	out, err := e.handle(context.Background(), ec)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var resp Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("response is not a Response envelope: %v (%s)", err, out)
	}
	return resp
}

// capture records the identity an operation was dispatched with.
func capture(r *toolbridge.Registry, skill, tool string, into *mcpclient.Identity) {
	r.AddOpForTest(skill, tool, func(ctx context.Context, _ json.RawMessage) (any, error) {
		id, _ := mcpclient.CallerFrom(ctx)
		*into = id
		return map[string]any{"ok": true}, nil
	})
}

// ★ The bearer is what an operation runs as, so how it is found is the whole
// contract this package owns. Three sources, in precedence order.
func TestBearerReachesTheOperation(t *testing.T) {
	t.Run("authorization header", func(t *testing.T) {
		e, reg := newAuthedStack(t)
		var got mcpclient.Identity
		capture(reg, "skill-x", "probe", &got)

		ec := execCtxFor(`{"skill":"skill-x","tool":"probe"}`)
		ec.ServiceParams = a2asrv.NewServiceParams(map[string][]string{
			"Authorization": {"Bearer header-token"},
		})
		if resp := dispatch(t, e, ec); !resp.OK {
			t.Fatalf("refused: %s", resp.Error)
		}
		if got.Bearer != "header-token" {
			t.Errorf("bearer = %q", got.Bearer)
		}
	})

	t.Run("envelope field", func(t *testing.T) {
		e, reg := newAuthedStack(t)
		var got mcpclient.Identity
		capture(reg, "skill-x", "probe", &got)

		dispatch(t, e, execCtxFor(`{"skill":"skill-x","tool":"probe","bearer":"envelope-token"}`))
		if got.Bearer != "envelope-token" {
			t.Errorf("bearer = %q", got.Bearer)
		}
	})

	t.Run("header beats envelope", func(t *testing.T) {
		e, reg := newAuthedStack(t)
		var got mcpclient.Identity
		capture(reg, "skill-x", "probe", &got)

		ec := execCtxFor(`{"skill":"skill-x","tool":"probe","bearer":"envelope-token"}`)
		ec.ServiceParams = a2asrv.NewServiceParams(map[string][]string{
			"Authorization": {"Bearer header-token"},
		})
		dispatch(t, e, ec)
		if got.Bearer != "header-token" {
			t.Errorf("bearer = %q, want the header to win", got.Bearer)
		}
	})
}

// The conversation id rides along even with no bearer. It is what mcpclient
// pools an MCP session by, which is how the card's promise that a bearer binds
// to the conversation survives without this process storing one.
func TestConversationIDRidesEvenUnauthenticated(t *testing.T) {
	e, reg := newAuthedStack(t)
	var got mcpclient.Identity
	capture(reg, "skill-x", "probe", &got)

	ec := execCtxFor(`{"skill":"skill-x","tool":"probe"}`)
	ec.ContextID = "conv-1"
	dispatch(t, e, ec)

	if got.ContextID != "conv-1" {
		t.Errorf("context id = %q", got.ContextID)
	}
	if got.Bearer != "" {
		t.Errorf("invented a bearer: %q", got.Bearer)
	}
}

func TestClientIPComesFromTheFirstForwardedHop(t *testing.T) {
	e, reg := newAuthedStack(t)
	var got mcpclient.Identity
	capture(reg, "skill-x", "probe", &got)

	ec := execCtxFor(`{"skill":"skill-x","tool":"probe"}`)
	ec.ServiceParams = a2asrv.NewServiceParams(map[string][]string{
		"X-Forwarded-For": {"203.0.113.7, 10.0.0.1"},
	})
	dispatch(t, e, ec)

	if got.ClientIP != "203.0.113.7" {
		t.Errorf("client ip = %q, want the first hop", got.ClientIP)
	}
}

func TestDispatchRejectsSkillToolMismatch(t *testing.T) {
	e, _ := newAuthedStack(t)
	if _, err := e.handle(context.Background(), execCtxFor(
		`{"skill":"svpchain-market-data","tool":"whoami"}`)); err == nil ||
		!strings.Contains(err.Error(), "belongs to skill") {
		t.Errorf("skill/tool mismatch must be named, got %v", err)
	}
}

func TestDispatchRejectsUnknownTool(t *testing.T) {
	e, _ := newAuthedStack(t)
	if _, err := e.handle(context.Background(), execCtxFor(
		`{"skill":"svpchain-account","tool":"nope"}`)); err == nil ||
		!strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("unknown tool must be named, got %v", err)
	}
}

// probe is a stand-in identity for card assertions that are about structure,
// not product identity.
var probe = CardIdentity{
	Name:        "card-probe",
	Version:     "0.0.0",
	Description: "Fixed identity, so this test reacts only to structural changes.",
}

// The card must advertise exactly what the executor will dispatch.
func TestCardMatchesRegistry(t *testing.T) {
	reg := toolbridge.NewRemote(nil)
	card := BuildAgentCardFor(probe, "https://agents.example.test", reg)

	bySkill := reg.BySkill()
	seen := map[string]bool{}
	for _, sk := range card.Skills {
		seen[sk.ID] = true
		for _, tool := range bySkill[sk.ID] {
			if !strings.Contains(sk.Description, tool) {
				t.Errorf("skill %s description does not name tool %s", sk.ID, tool)
			}
		}
	}
	for skill := range bySkill {
		if !seen[skill] {
			t.Errorf("registry serves skill %q that the card does not advertise", skill)
		}
	}
}

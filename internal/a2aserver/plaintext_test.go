package a2aserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// stubAsk records what the assistant skill was asked, without a model or an
// MCP server behind it.
type stubAsk struct{ got string }

func (s *stubAsk) register(r *toolbridge.Registry) {
	r.AddOpForTest(toolbridge.SkillAssistant, "ask", func(_ context.Context, args json.RawMessage) (any, error) {
		var in toolbridge.AskInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		s.got = in.Question
		return map[string]any{"text": "answered"}, nil
	})
}

// ★ Another agent asking a question in English is the normal case, not a
// malformed envelope. Refusing it while advertising a skill whose whole job is
// answering it is what made this agent the only one in the fleet that could
// not take a question.
func TestPlainTextBecomesAnAssistantQuestion(t *testing.T) {
	e, _, _ := newAuthedStack(t)
	stub := &stubAsk{}
	stub.register(e.registry)

	const question = "Do you offer a tool to look up an account's balance on svpchain?"
	resp := dispatch(t, e, execCtxFor(question))

	if !resp.OK {
		t.Fatalf("plain text was refused: %s", resp.Error)
	}
	if resp.Skill != toolbridge.SkillAssistant || resp.Tool != "ask" {
		t.Errorf("routed to %s/%s, want the assistant", resp.Skill, resp.Tool)
	}
	if stub.got != question {
		t.Errorf("the assistant received %q, want the message verbatim", stub.got)
	}
}

// Without an assistant the refusal has to say what would have worked. The
// original said only "request must be JSON naming a skill", which leaves a
// caller with nothing to try.
func TestPlainTextWithoutAnAssistantExplainsTheEnvelope(t *testing.T) {
	e, _, _ := newAuthedStack(t) // no assistant registered

	_, err := e.handle(context.Background(), execCtxFor("what is my balance?"))
	if err == nil {
		t.Fatal("plain text was accepted with no assistant to answer it")
	}
	for _, want := range []string{`"skill"`, `"tool"`, "list_tools"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "plain-English") {
		t.Error("refusal offers plain English on an agent that serves no assistant")
	}
}

// And with one, the refusal for a malformed envelope should say plain English
// is available.
func TestEnvelopeErrorMentionsPlainEnglishWhenServed(t *testing.T) {
	e, _, _ := newAuthedStack(t)
	(&stubAsk{}).register(e.registry)

	// Valid JSON, but not an envelope: not a question, so it stays an error.
	_, err := e.handle(context.Background(), execCtxFor(`{"foo":1}`))
	if err == nil {
		t.Fatal("a JSON object naming no skill was accepted")
	}
	if !strings.Contains(err.Error(), "plain-English") {
		t.Errorf("refusal does not mention the assistant: %v", err)
	}
}

func TestEmptyMessageIsNotAQuestion(t *testing.T) {
	e, _, _ := newAuthedStack(t)
	stub := &stubAsk{}
	stub.register(e.registry)

	if _, err := e.handle(context.Background(), execCtxFor("   ")); err == nil {
		t.Error("whitespace was routed as a question")
	}
	if stub.got != "" {
		t.Errorf("the assistant was asked %q", stub.got)
	}
}

// A caller that names its tool must never reach a model: that is the whole
// point of the envelope staying a lookup.
func TestNamedToolStillBypassesTheAssistant(t *testing.T) {
	e, _, _ := newAuthedStack(t)
	stub := &stubAsk{}
	stub.register(e.registry)

	resp := dispatch(t, e, execCtxFor(`{"skill":"svpchain-account","tool":"whoami"}`))
	if resp.Tool != "whoami" {
		t.Errorf("routed to %q, want whoami", resp.Tool)
	}
	if stub.got != "" {
		t.Errorf("a named tool reached the assistant: %q", stub.got)
	}
}

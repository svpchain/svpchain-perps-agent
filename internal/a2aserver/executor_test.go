package a2aserver

import (
	"context"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

func execCtxFor(raw string) *a2asrv.ExecutorContext {
	return &a2asrv.ExecutorContext{
		Message: &a2a.Message{Parts: a2a.ContentParts{a2a.NewTextPart(raw)}},
	}
}

func newTestExecutor() *Executor {
	return NewFullExecutor(toolbridge.NewRemote(nil), nil)
}

func TestHandleRejectsBadRequests(t *testing.T) {
	e := newTestExecutor()
	cases := map[string]string{
		"no skill":  `{"tool":"list_markets"}`,
		"no tool":   `{"skill":"svpchain-market-data"}`,
		"not JSON":  `hello`,
		"empty":     ``,
		"bad skill": `{"skill":"nope","tool":"list_markets"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := e.handle(context.Background(), execCtxFor(raw)); err == nil {
				t.Errorf("%q was accepted", raw)
			}
		})
	}
}

// ★ The {"skill":…,"query":…} read layer is gone: it answered from a direct
// indexer client with no credential, and every tool on the MCP server is
// bearer-gated, so there was nothing to move it onto. A caller still on that
// form must be told which tool replaced its query rather than getting a
// generic parse failure.
func TestLegacyQueryFormIsRefusedWithAMigrationHint(t *testing.T) {
	e := newTestExecutor()

	_, err := e.handle(context.Background(), execCtxFor(
		`{"skill":"svpchain-market-data","query":"estimate","ticker":"BTC-USD","side":"buy","size":"2.5"}`))
	if err == nil {
		t.Fatal("the legacy query form was accepted")
	}
	for _, want := range []string{"list_markets", toolbridge.EstimateClearingPrice, "bearer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// The estimate is this agent's own operation, not a proxied one, so it has to
// be on the surface the card advertises.
func TestEstimateIsRegisteredUnderMarketData(t *testing.T) {
	op, ok := toolbridge.NewRemote(nil).Lookup(toolbridge.EstimateClearingPrice)
	if !ok {
		t.Fatal("estimate_clearing_price is not registered")
	}
	if op.Skill != toolbridge.SkillMarketData {
		t.Errorf("registered under %q, want %q", op.Skill, toolbridge.SkillMarketData)
	}
	if op.InputSchema == nil {
		t.Error("no argument schema, so a caller cannot discover how to call it")
	}
}

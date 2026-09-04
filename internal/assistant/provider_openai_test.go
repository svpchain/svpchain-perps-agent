package assistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeOpenAI is an endpoint speaking the chat-completions format, standing in
// for OpenAI, DeepSeek, or anything else serving it.
type fakeOpenAI struct {
	mu       sync.Mutex
	script   []string
	requests []map[string]any
	status   int
	rawBody  string // when set, returned instead of the script
}

func startFakeOpenAI(t *testing.T, script ...string) (*fakeOpenAI, *OpenAIProvider) {
	t.Helper()
	f := &fakeOpenAI{script: script, status: http.StatusOK}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("posted to %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)

		f.mu.Lock()
		f.requests = append(f.requests, parsed)
		n := len(f.requests) - 1
		status, raw := f.status, f.rawBody
		var out string
		if n < len(f.script) {
			out = f.script[n]
		}
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if raw != "" {
			_, _ = io.WriteString(w, raw)
			return
		}
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(ts.Close)

	p, err := NewOpenAIProvider(OpenAIConfig{BaseURL: ts.URL, APIKey: "test-key", Model: "deepseek-v4-pro"})
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}

func (f *fakeOpenAI) request(i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func oaToolCallReply(id, name, argsJSONString string) string {
	// arguments is a JSON *string* holding an object, which is the format's
	// defining quirk and the thing most likely to be got wrong.
	args, _ := json.Marshal(argsJSONString)
	return `{"id":"c","object":"chat.completion","model":"deepseek-v4-pro",
	 "choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
	   "tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(args) + `}}]}}],
	 "usage":{"prompt_tokens":11,"completion_tokens":7}}`
}

func oaTextReply(text string) string {
	b, _ := json.Marshal(text)
	return `{"id":"c","object":"chat.completion","model":"deepseek-v4-pro",
	 "choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":` + string(b) + `}}],
	 "usage":{"prompt_tokens":11,"completion_tokens":7}}`
}

func sampleTools() []ToolDef {
	return []ToolDef{{
		Name:        "get_subaccount",
		Description: "read a subaccount",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"address": map[string]any{"type": "string", "description": "the svp1 owner address"},
			},
			"required": []any{"address"},
		},
	}}
}

// The declaration shape: type/function/name/description/parameters, with the
// whole schema as parameters so `required` survives.
func TestOpenAIToolDeclarationShape(t *testing.T) {
	f, p := startFakeOpenAI(t, oaTextReply("hi"))
	s := p.Start("be helpful", sampleTools())

	if _, err := s.Send(context.Background(), Input{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	tools, _ := f.request(0)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("sent %d tools, want 1", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf(`tool type = %v, want "function"`, tool["type"])
	}
	fn, _ := tool["function"].(map[string]any)
	if fn["name"] != "get_subaccount" {
		t.Errorf("name = %v", fn["name"])
	}
	params, _ := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("parameters.type = %v", params["type"])
	}
	// ★ The field an earlier version dropped.
	req, _ := params["required"].([]any)
	if len(req) != 1 || req[0] != "address" {
		t.Errorf("required = %v, want [address]", params["required"])
	}
}

// The system prompt is an ordinary first message in this format, not a field.
func TestOpenAISystemPromptIsTheFirstMessage(t *testing.T) {
	f, p := startFakeOpenAI(t, oaTextReply("hi"))
	s := p.Start("be helpful", sampleTools())
	if _, err := s.Send(context.Background(), Input{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := f.request(0)["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be helpful" {
		t.Errorf("first message = %+v, want the system prompt", first)
	}
	if _, has := f.request(0)["system"]; has {
		t.Error("sent a top-level system field, which this format does not have")
	}
}

// ★ arguments arrives as a JSON string, not an object.
func TestOpenAIToolCallArgumentsAreUnwrappedFromTheirString(t *testing.T) {
	_, p := startFakeOpenAI(t, oaToolCallReply("call_1", "get_subaccount", `{"address":"svp1abc"}`))
	s := p.Start("sys", sampleTools())

	reply, err := s.Send(context.Background(), Input{Text: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Stop != StopToolUse {
		t.Errorf("stop = %q, want %q", reply.Stop, StopToolUse)
	}
	if len(reply.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls", len(reply.ToolCalls))
	}
	var args struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(reply.ToolCalls[0].Args, &args); err != nil {
		t.Fatalf("arguments did not decode as JSON: %v", err)
	}
	if args.Address != "svp1abc" {
		t.Errorf("address = %q", args.Address)
	}
}

// The docs warn the model may emit invalid JSON here. That must become an
// empty argument object the tool can refuse, not a failed request.
func TestOpenAIInvalidToolArgumentsBecomeAnEmptyObject(t *testing.T) {
	_, p := startFakeOpenAI(t, oaToolCallReply("call_1", "get_subaccount", `{"address": `))
	s := p.Start("sys", sampleTools())

	reply, err := s.Send(context.Background(), Input{Text: "q"})
	if err != nil {
		t.Fatalf("malformed arguments failed the whole request: %v", err)
	}
	if got := string(reply.ToolCalls[0].Args); got != "{}" {
		t.Errorf("args = %s, want {}", got)
	}
}

// Tool results go back under their own role, naming the call they answer.
func TestOpenAIToolResultsUseTheToolRole(t *testing.T) {
	f, p := startFakeOpenAI(t,
		oaToolCallReply("call_1", "get_subaccount", `{"address":"svp1abc"}`),
		oaTextReply("your balance is 42"),
	)
	s := p.Start("sys", sampleTools())

	if _, err := s.Send(context.Background(), Input{Text: "q"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), Input{
		ToolResults: []ToolResult{{ID: "call_1", Content: `{"value":"42"}`}},
	}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := f.request(1)["messages"].([]any)
	var found bool
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] != "tool" {
			continue
		}
		found = true
		if mm["tool_call_id"] != "call_1" {
			t.Errorf("tool_call_id = %v, want call_1", mm["tool_call_id"])
		}
		if mm["content"] != `{"value":"42"}` {
			t.Errorf("content = %v", mm["content"])
		}
	}
	if !found {
		t.Error("no tool-role message was sent")
	}
	// The assistant turn that asked for it must still be in the transcript,
	// or the tool result answers nothing.
	var sawAssistantToolCall bool
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] == "assistant" && mm["tool_calls"] != nil {
			sawAssistantToolCall = true
		}
	}
	if !sawAssistantToolCall {
		t.Error("the assistant's tool_calls turn was dropped from the transcript")
	}
}

func TestOpenAIFinalTurnWithholdsTools(t *testing.T) {
	f, p := startFakeOpenAI(t, oaTextReply("partial answer"))
	s := p.Start("sys", sampleTools())

	if _, err := s.Send(context.Background(), Input{Text: "q", FinalTurn: true}); err != nil {
		t.Fatal(err)
	}
	if _, has := f.request(0)["tools"]; has {
		t.Error("the final turn still offered tools")
	}
}

func TestOpenAIFinishReasonMapping(t *testing.T) {
	for reason, want := range map[string]StopReason{
		"stop":           StopEnd,
		"tool_calls":     StopToolUse,
		"length":         StopLength,
		"content_filter": StopRefusal,
		"":               StopEnd,
	} {
		body := `{"choices":[{"index":0,"finish_reason":"` + reason + `","message":{"role":"assistant","content":"x"}}],"usage":{}}`
		_, p := startFakeOpenAI(t, body)
		s := p.Start("sys", nil)
		reply, err := s.Send(context.Background(), Input{Text: "q"})
		if err != nil {
			t.Fatalf("%q: %v", reason, err)
		}
		if reply.Stop != want {
			t.Errorf("finish_reason %q mapped to %q, want %q", reason, reply.Stop, want)
		}
	}
}

// ★ An upstream failure body reaches the A2A caller through the error string,
// so it must not be echoed: a misconfigured base URL can return anything.
func TestOpenAIErrorDoesNotEchoTheResponseBody(t *testing.T) {
	f, p := startFakeOpenAI(t)
	f.mu.Lock()
	f.status = http.StatusUnauthorized
	f.rawBody = "<html>secret-internal-hostname-and-token</html>"
	f.mu.Unlock()

	s := p.Start("sys", sampleTools())
	_, err := s.Send(context.Background(), Input{Text: "q"})
	if err == nil {
		t.Fatal("a 401 was not reported as an error")
	}
	if strings.Contains(err.Error(), "secret-internal-hostname") {
		t.Errorf("the error echoed the upstream body: %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("the error does not say what happened: %v", err)
	}
}

func TestOpenAIProviderRequiresKeyAndModel(t *testing.T) {
	if _, err := NewOpenAIProvider(OpenAIConfig{Model: "m"}); err == nil {
		t.Error("accepted an empty API key")
	}
	if _, err := NewOpenAIProvider(OpenAIConfig{APIKey: "k"}); err == nil {
		t.Error("accepted an empty model; this format has no cross-vendor default")
	}
}

func TestOpenAIDefaultsToOpenAIsOwnEndpoint(t *testing.T) {
	p, err := NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if p.baseURL != DefaultOpenAIBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, DefaultOpenAIBaseURL)
	}
}

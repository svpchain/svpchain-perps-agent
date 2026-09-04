package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultOpenAIBaseURL is OpenAI's own endpoint. DeepSeek serves this same
// format at https://api.deepseek.com, so pointing base_url there and naming a
// deepseek model is the whole difference between the two.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIProvider runs the planner on the chat-completions format.
//
// Written against the format rather than against one vendor's SDK, because the
// point of choosing it is that several vendors serve it: OpenAI, DeepSeek, and
// most local runtimes. A vendor SDK would tie this to whichever one it came
// from, and the surface actually used here — one non-streaming call with tools
// — is small enough that the tests can pin the exact JSON.
//
// ★ It does not cache. The format has no equivalent of a cache breakpoint, so
// the tool catalog is re-billed on every turn of every planning loop. That is
// the standing cost of provider portability, and it is why the Anthropic path
// remains the default rather than being replaced.
type OpenAIProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
	model   string
}

// OpenAIConfig points the provider at an endpoint.
type OpenAIConfig struct {
	// BaseURL is the API root, without a trailing slash. Empty means OpenAI.
	BaseURL string

	// APIKey authenticates. Sent as a bearer token, which is what every
	// implementation of this format expects.
	APIKey string

	Model      string
	HTTPClient *http.Client
}

// NewOpenAIProvider returns a provider for any endpoint serving the
// chat-completions format.
func NewOpenAIProvider(cfg OpenAIConfig) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required; this format has no sensible default across vendors")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultOpenAIBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &OpenAIProvider{
		client:  client,
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
	}, nil
}

func (p *OpenAIProvider) Name() string  { return ProviderOpenAI }
func (p *OpenAIProvider) Model() string { return p.model }

func (p *OpenAIProvider) Start(system string, tools []ToolDef) Session {
	defs := make([]oaTool, 0, len(tools))
	for _, t := range tools {
		// The whole schema object goes across as `parameters`, so `required`
		// survives — unlike the Anthropic definition, which splits it out.
		schema := t.Schema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		defs = append(defs, oaTool{
			Type: "function",
			Function: oaToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}
	s := &openAISession{p: p, tools: defs}
	// The system prompt is an ordinary message in this format, first in the
	// list, rather than a field of its own.
	s.messages = append(s.messages, oaMessage{Role: "system", Content: system})
	return s
}

// ---- wire types ----

type oaToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type oaTool struct {
	Type     string     `json:"type"`
	Function oaToolFunc `json:"function"`
}

// oaToolCall is one requested call. Arguments is a JSON *string* holding an
// object, not an object — a quirk of the format, and the reason the planner's
// arguments have to be unquoted on the way in and re-quoted on the way out.
type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role      string       `json:"role"`
	Content   string       `json:"content,omitempty"`
	ToolCalls []oaToolCall `json:"tool_calls,omitempty"`

	// ToolCallID is set only on a tool-result message, which this format
	// carries as its own role rather than as part of a user turn.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type oaRequest struct {
	Model     string      `json:"model"`
	Messages  []oaMessage `json:"messages"`
	Tools     []oaTool    `json:"tools,omitempty"`
	MaxTokens int64       `json:"max_tokens,omitempty"`
}

type oaResponse struct {
	Choices []struct {
		Message      oaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ---- session ----

type openAISession struct {
	p        *OpenAIProvider
	tools    []oaTool
	messages []oaMessage
}

func (s *openAISession) Send(ctx context.Context, in Input) (*Reply, error) {
	if in.Text != "" {
		s.messages = append(s.messages, oaMessage{Role: "user", Content: in.Text})
	}
	// One message per result, each naming the call it answers. The single-turn
	// rule that matters on the Anthropic side has no equivalent here: these
	// are separate messages by construction.
	for _, r := range in.ToolResults {
		s.messages = append(s.messages, oaMessage{
			Role:       "tool",
			ToolCallID: r.ID,
			Content:    r.Content,
		})
	}

	req := oaRequest{
		Model:     s.p.model,
		Messages:  s.messages,
		MaxTokens: maxTokensFrom(ctx),
	}
	if !in.FinalTurn {
		req.Tools = s.tools
	}

	resp, err := s.p.post(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("the model returned no choices")
	}
	choice := resp.Choices[0]
	s.messages = append(s.messages, choice.Message)

	reply := &Reply{
		Text:         strings.TrimSpace(choice.Message.Content),
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		Stop:         openAIStop(choice.FinishReason),
	}
	for _, tc := range choice.Message.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		// The docs warn the model does not always produce valid JSON here.
		// An unparseable argument object becomes an empty one rather than a
		// failed request: the tool then refuses on its own terms and the model
		// can correct itself, which is the same path every other tool error
		// takes.
		if !json.Valid(args) {
			args = json.RawMessage(`{}`)
		}
		reply.ToolCalls = append(reply.ToolCalls, ToolRequest{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}
	return reply, nil
}

func (s *openAISession) post(ctx context.Context, body oaRequest) (*oaResponse, error) {
	return s.p.post(ctx, body)
}

func (p *OpenAIProvider) post(ctx context.Context, body oaRequest) (*oaResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var decoded oaResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// ★ Never echo the body: a failing endpoint can put anything in it,
		// and this text reaches the A2A caller. The status is enough to act on.
		return nil, fmt.Errorf("model API returned %d with an unreadable body", res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		if decoded.Error != nil {
			return nil, fmt.Errorf("model API %d: %s", res.StatusCode, decoded.Error.Message)
		}
		return nil, fmt.Errorf("model API returned %d", res.StatusCode)
	}
	return &decoded, nil
}

func openAIStop(reason string) StopReason {
	switch reason {
	case "tool_calls", "function_call":
		return StopToolUse
	case "length":
		return StopLength
	case "content_filter":
		return StopRefusal
	default:
		return StopEnd
	}
}

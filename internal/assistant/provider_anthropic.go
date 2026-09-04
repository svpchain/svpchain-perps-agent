package assistant

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// AnthropicProvider runs the planner on the native Messages API.
//
// It is the default because of one feature the compatibility layers do not
// carry: prompt caching. The tool block is 30-odd schemas, byte-identical on
// every request of every conversation, and caching it is the difference
// between paying for that catalog once and paying for it on every turn of
// every planning loop.
//
// It also reaches DeepSeek unchanged, which serves this same format on its own
// path — but cache_control is ignored there, so that route trades the caching
// away too.
type AnthropicProvider struct {
	client anthropic.Client
	model  string
}

// NewAnthropicProvider wraps an already-configured client. The client reads
// ANTHROPIC_BASE_URL, so pointing it at another vendor's Anthropic-compatible
// endpoint needs no code here.
func NewAnthropicProvider(client anthropic.Client, model string) *AnthropicProvider {
	if model == "" {
		model = DefaultModel
	}
	return &AnthropicProvider{client: client, model: model}
}

func (p *AnthropicProvider) Name() string  { return ProviderAnthropic }
func (p *AnthropicProvider) Model() string { return p.model }

func (p *AnthropicProvider) Start(system string, tools []ToolDef) Session {
	defs := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schema := anthropic.ToolInputSchemaParam{Properties: t.Properties()}
		if req := t.Required(); len(req) > 0 {
			schema.Required = req
		}
		def := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: schema,
		}
		defs = append(defs, anthropic.ToolUnionParam{OfTool: &def})
	}
	return &anthropicSession{p: p, system: system, tools: defs}
}

// anthropicSession keeps the transcript in the API's own shape, so an
// assistant turn is replayed exactly as it came back rather than rebuilt.
type anthropicSession struct {
	p        *AnthropicProvider
	system   string
	tools    []anthropic.ToolUnionParam
	messages []anthropic.MessageParam
}

func (s *anthropicSession) Send(ctx context.Context, in Input) (*Reply, error) {
	if in.Text != "" {
		s.messages = append(s.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(in.Text)))
	}
	if len(in.ToolResults) > 0 {
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(in.ToolResults))
		for _, r := range in.ToolResults {
			blocks = append(blocks, anthropic.NewToolResultBlock(r.ID, r.Content, r.IsError))
		}
		s.messages = append(s.messages, anthropic.NewUserMessage(blocks...))
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(s.p.model),
		MaxTokens: maxTokensFrom(ctx),
		Messages:  s.messages,
		// Tools render before system, so one breakpoint on the last system
		// block caches the whole stable prefix.
		System: []anthropic.TextBlockParam{{
			Text:         s.system,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
	}
	if !in.FinalTurn {
		params.Tools = s.tools
	}

	resp, err := s.p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}
	s.messages = append(s.messages, resp.ToParam())

	reply := &Reply{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		Stop:         anthropicStop(resp.StopReason),
	}
	var text strings.Builder
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			if v.Text != "" {
				if text.Len() > 0 {
					text.WriteString("\n")
				}
				text.WriteString(v.Text)
			}
		case anthropic.ToolUseBlock:
			reply.ToolCalls = append(reply.ToolCalls, ToolRequest{
				ID:   block.ID,
				Name: v.Name,
				Args: json.RawMessage(v.JSON.Input.Raw()),
			})
		}
	}
	reply.Text = strings.TrimSpace(text.String())
	return reply, nil
}

func anthropicStop(r anthropic.StopReason) StopReason {
	switch r {
	case anthropic.StopReasonToolUse:
		return StopToolUse
	case anthropic.StopReasonRefusal:
		return StopRefusal
	case anthropic.StopReasonMaxTokens:
		return StopLength
	default:
		return StopEnd
	}
}

// maxTokensFrom carries the per-request output cap on the context rather than
// in Input, so it does not have to appear in every provider's turn struct.
type maxTokensKey struct{}

func withMaxTokens(ctx context.Context, n int64) context.Context {
	return context.WithValue(ctx, maxTokensKey{}, n)
}

func maxTokensFrom(ctx context.Context) int64 {
	if n, ok := ctx.Value(maxTokensKey{}).(int64); ok && n > 0 {
		return n
	}
	return DefaultMaxTokens
}

package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GenAI span helpers. Facts validated live (box tenant):
//   - gen_ai.operation.name discriminates chat / execute_tool / invoke_agent.
//   - Chat spans carry the whole exchange: gen_ai.input.messages,
//     gen_ai.output.messages and gen_ai.system_instructions are JSON strings
//     of [{role, parts: [{type, content|name+arguments}]}] with part types
//     text, reasoning, tool_call (id/name/arguments), tool_call_response.
//   - Tool spans carry gen_ai.tool.name / gen_ai.tool.call.arguments /
//     gen_ai.tool.call.result; agent spans gen_ai.agent.name and
//     gen_ai.conversation.id.
//   - Token usage: gen_ai.usage.input_tokens / .output_tokens plus
//     .cache_read.input_tokens / .cache_creation.input_tokens.
//   - Spans link to Smartscape via dt.smartscape.gen_ai.model / .provider /
//     .service / .agent (dot namespace — see smartscapeField).

// GenAIOp returns the span's GenAI operation ("" for non-GenAI spans). Two
// conventions exist in the wild (both validated live): the current semconv
// carries gen_ai.operation.name; the older traceloop/LangChain style has no
// operation name — llm.request.type discriminates (chat/completion/…) and
// the exchange lives in flat numbered attributes (gen_ai.prompt.0.role, …).
func GenAIOp(rec map[string]any) string {
	if op := Str(rec, "gen_ai.operation.name"); op != "" {
		return op
	}
	if t := Str(rec, "llm.request.type"); t != "" {
		return t
	}
	if Str(rec, "gen_ai.prompt.0.role") != "" {
		return "chat"
	}
	return ""
}

// GenAIOpShort compresses the operation name for a badge-width column.
func GenAIOpShort(op string) string {
	switch op {
	case "execute_tool":
		return "tool"
	case "invoke_agent":
		return "agent"
	case "text_completion":
		return "text"
	case "embeddings":
		return "embed"
	}
	return op // chat, …
}

// GenAIModel returns the span's model, preferring the requested one.
func GenAIModel(rec map[string]any) string {
	if m := Str(rec, "gen_ai.request.model"); m != "" {
		return m
	}
	return Str(rec, "gen_ai.response.model")
}

// GenAITokens renders the span's token usage as "in→out" ("" when absent).
// A cache-read share is folded into the in count — it is real context. The
// cache key differs per convention: .cache_read.input_tokens (semconv) vs
// .cache_read_input_tokens (traceloop) — both validated live.
func GenAITokens(rec map[string]any) string {
	in, inOK := FloatValue(rec["gen_ai.usage.input_tokens"])
	for _, key := range []string{"gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens"} {
		if cached, ok := FloatValue(rec[key]); ok {
			in += cached
			inOK = true
			break
		}
	}
	out, outOK := FloatValue(rec["gen_ai.usage.output_tokens"])
	if !inOK && !outOK {
		return ""
	}
	return AbbrevCount(in) + "→" + AbbrevCount(out)
}

// AbbrevCount abbreviates a count for a narrow cell (97479 → "97.5k").
func AbbrevCount(f float64) string {
	switch {
	case f >= 1e9:
		return fmt.Sprintf("%.1fG", f/1e9)
	case f >= 1e6:
		return fmt.Sprintf("%.1fM", f/1e6)
	case f >= 10e3:
		return fmt.Sprintf("%.0fk", f/1e3)
	case f >= 1e3:
		return fmt.Sprintf("%.1fk", f/1e3)
	}
	return fmt.Sprintf("%.0f", f)
}

// genaiDetailKey memoizes the (potentially expensive) per-row detail text —
// column Value funcs run on every render frame.
const genaiDetailKey = "__genai.detail"

// GenAIDetail is the row's most telling GenAI text: the tool call
// (name + arguments), the last user prompt of a chat, or the agent name.
func GenAIDetail(rec map[string]any) string {
	if cached, ok := rec[genaiDetailKey].(string); ok {
		return cached
	}
	detail := genaiDetail(rec)
	rec[genaiDetailKey] = detail
	return detail
}

func genaiDetail(rec map[string]any) string {
	switch GenAIOp(rec) {
	case "execute_tool":
		name := Str(rec, "gen_ai.tool.name")
		args := compactText(Str(rec, "gen_ai.tool.call.arguments"), 160)
		switch {
		case name != "" && args != "":
			return name + " " + args
		case name != "":
			return name
		}
	case "invoke_agent":
		if name := Str(rec, "gen_ai.agent.name"); name != "" {
			return name
		}
	default:
		// The prompt: the last user message of the exchange (chat spans
		// carry the whole history — the last user turn is this call's ask).
		msgs := GenAIInput(rec)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role != "user" {
				continue
			}
			if text := msgs[i].Text(); text != "" {
				return compactText(text, 300)
			}
		}
		// No user text (an agent hand-off turn): the model's tool call is
		// the story.
		for _, m := range GenAIOutput(rec) {
			for _, p := range m.Parts {
				if p.Type == "tool_call" && p.Name != "" {
					return compactText(p.Name+" "+p.Content, 160)
				}
			}
		}
	}
	return ""
}

// GenAIInput returns the prompt-side messages under either convention:
// the semconv JSON blob, or the traceloop flat numbered attributes.
func GenAIInput(rec map[string]any) []GenAIMessage {
	if msgs := ParseGenAIMessages(rec["gen_ai.input.messages"]); len(msgs) > 0 {
		return msgs
	}
	return FlatGenAIMessages(rec, "gen_ai.prompt")
}

// GenAIOutput returns the completion-side messages under either convention.
func GenAIOutput(rec map[string]any) []GenAIMessage {
	if msgs := ParseGenAIMessages(rec["gen_ai.output.messages"]); len(msgs) > 0 {
		return msgs
	}
	return FlatGenAIMessages(rec, "gen_ai.completion")
}

// flatMessageMax bounds the numbered-attribute scan (a runaway guard, far
// above any real exchange).
const flatMessageMax = 500

// FlatGenAIMessages assembles messages from the flat numbered convention —
// gen_ai.prompt.0.role / .content / .tool_calls.0.name/.arguments — used by
// traceloop-style instrumentations (validated live on the demo tenant).
func FlatGenAIMessages(rec map[string]any, prefix string) []GenAIMessage {
	var msgs []GenAIMessage
	for i := 0; i < flatMessageMax; i++ {
		base := fmt.Sprintf("%s.%d.", prefix, i)
		m := GenAIMessage{Role: Str(rec, base+"role")}
		if content := Str(rec, base+"content"); content != "" {
			m.Parts = append(m.Parts, GenAIPart{Type: "text", Content: content})
		}
		for j := 0; j < flatMessageMax; j++ {
			tc := fmt.Sprintf("%stool_calls.%d.", base, j)
			name, args := Str(rec, tc+"name"), Str(rec, tc+"arguments")
			if name == "" && args == "" {
				break
			}
			m.Parts = append(m.Parts, GenAIPart{Type: "tool_call", Name: name, Content: args})
		}
		if m.Role == "" && len(m.Parts) == 0 {
			break // indices are contiguous — a gap ends the exchange
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// compactText squashes whitespace runs and truncates to max bytes.
func compactText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// GenAIMessage is one turn of a chat exchange.
type GenAIMessage struct {
	Role  string
	Parts []GenAIPart
}

// GenAIPart is one part of a message: plain text, model reasoning, a tool
// call, or a tool result.
type GenAIPart struct {
	Type    string // text, reasoning, tool_call, tool_call_response, …
	Name    string // tool name (tool_call parts)
	Content string // text content, or the part's payload as compact JSON
}

// Text concatenates the message's text parts.
func (m GenAIMessage) Text() string {
	var parts []string
	for _, p := range m.Parts {
		if p.Type == "text" && p.Content != "" {
			parts = append(parts, p.Content)
		}
	}
	return strings.Join(parts, "\n")
}

// ParseGenAIMessages decodes a gen_ai.*.messages value — a JSON string of
// [{role, parts}] (validated live). Unparseable input yields nil.
func ParseGenAIMessages(v any) []GenAIMessage {
	raw, ok := v.(string)
	if !ok || raw == "" {
		return nil
	}
	var decoded []struct {
		Role  string           `json:"role"`
		Parts []map[string]any `json:"parts"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	msgs := make([]GenAIMessage, 0, len(decoded))
	for _, d := range decoded {
		m := GenAIMessage{Role: d.Role}
		for _, p := range d.Parts {
			part := GenAIPart{Type: Str(p, "type"), Name: Str(p, "name")}
			if c, ok := p["content"].(string); ok {
				part.Content = c
			} else {
				// tool_call arguments / tool results: compact the payload.
				payload := map[string]any{}
				for k, val := range p {
					if k != "type" && k != "id" && k != "name" {
						payload[k] = val
					}
				}
				if len(payload) > 0 {
					if b, err := json.Marshal(payload); err == nil {
						part.Content = string(b)
					}
				}
			}
			m.Parts = append(m.Parts, part)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// GenAISystemInstructions extracts the system prompt text of a chat span
// (gen_ai.system_instructions is a JSON string of [{type, content}]).
func GenAISystemInstructions(rec map[string]any) string {
	raw := Str(rec, "gen_ai.system_instructions")
	if raw == "" {
		return ""
	}
	var decoded []struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw // opaque but better shown than dropped
	}
	var parts []string
	for _, d := range decoded {
		if d.Content != "" {
			parts = append(parts, d.Content)
		}
	}
	return strings.Join(parts, "\n")
}
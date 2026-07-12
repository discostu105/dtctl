package catalog

import (
	"strings"
	"testing"
)

// The traceloop/LangChain convention (validated live on the demo tenant):
// no gen_ai.operation.name, llm.request.type discriminates, the exchange
// lives in flat numbered attributes.
func flatChatSpan() map[string]any {
	return map[string]any{
		"llm.request.type":                           "chat",
		"gen_ai.prompt.0.role":                       "system",
		"gen_ai.prompt.0.content":                    "You are a faq agent.",
		"gen_ai.prompt.1.role":                       "user",
		"gen_ai.prompt.1.content":                    "what is the maintenance state of the 787 fleet?",
		"gen_ai.prompt.2.role":                       "assistant",
		"gen_ai.prompt.2.content":                    "",
		"gen_ai.prompt.2.tool_calls.0.name":          "transfer_to_faq_agent",
		"gen_ai.prompt.2.tool_calls.0.arguments":     "{}",
		"gen_ai.completion.0.role":                   "assistant",
		"gen_ai.completion.0.tool_calls.0.name":      "faq",
		"gen_ai.completion.0.tool_calls.0.arguments": `{"query": "787 fleet"}`,
		"gen_ai.usage.input_tokens":                  "181",
		"gen_ai.usage.output_tokens":                 "26",
		"gen_ai.usage.cache_read_input_tokens":       "100",
		"gen_ai.request.model":                       "genai-demo",
	}
}

func TestGenAIFlatConvention(t *testing.T) {
	rec := flatChatSpan()
	if op := GenAIOp(rec); op != "chat" {
		t.Errorf("GenAIOp = %q, want chat (llm.request.type fallback)", op)
	}
	input := GenAIInput(rec)
	if len(input) != 3 {
		t.Fatalf("input messages = %d, want 3", len(input))
	}
	if input[0].Role != "system" || input[1].Role != "user" {
		t.Errorf("roles = %s/%s", input[0].Role, input[1].Role)
	}
	if len(input[2].Parts) != 1 || input[2].Parts[0].Type != "tool_call" || input[2].Parts[0].Name != "transfer_to_faq_agent" {
		t.Errorf("assistant tool_call part = %+v", input[2].Parts)
	}
	output := GenAIOutput(rec)
	if len(output) != 1 || output[0].Parts[0].Name != "faq" {
		t.Errorf("output = %+v", output)
	}
	// The detail column shows the last user prompt.
	if d := GenAIDetail(rec); !strings.Contains(d, "787 fleet?") {
		t.Errorf("detail = %q", d)
	}
	// Cache tokens fold into "in" under the traceloop key too.
	if tok := GenAITokens(rec); tok != "281→26" {
		t.Errorf("tokens = %q, want 281→26", tok)
	}
	// A tool-call-only exchange (agent hand-off): the tool call is the story.
	handoff := map[string]any{
		"llm.request.type":                           "chat",
		"gen_ai.prompt.0.role":                       "system",
		"gen_ai.prompt.0.content":                    "route requests",
		"gen_ai.completion.0.role":                   "assistant",
		"gen_ai.completion.0.tool_calls.0.name":      "transfer_to_pricing",
		"gen_ai.completion.0.tool_calls.0.arguments": "{}",
	}
	if d := GenAIDetail(handoff); !strings.Contains(d, "transfer_to_pricing") {
		t.Errorf("hand-off detail = %q", d)
	}
}

func TestGenAILensCoversBothConventions(t *testing.T) {
	lens := lensAt(spanLenses, DefaultSpanLens("GENAI_MODEL"))
	if lens.Name != "genai" {
		t.Fatalf("DefaultSpanLens(GENAI_MODEL) → %s", lens.Name)
	}
	for _, want := range []string{"gen_ai.operation.name", "llm.request.type"} {
		if !strings.Contains(lens.Filter, want) {
			t.Errorf("genai lens filter must cover %s:\n%s", want, lens.Filter)
		}
	}
	if got := lensAt(spanLenses, DefaultSpanLens("SERVICE")).Name; got != "all" {
		t.Errorf("DefaultSpanLens(SERVICE) → %s, want all (scoped roots is empty for most services)", got)
	}
}

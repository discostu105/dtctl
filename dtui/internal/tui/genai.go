package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// GenAI conversation rendering: a chat/tool span's exchange as first-class
// inspector rows — system prompt, every turn, tool calls and results —
// instead of opaque JSON blobs buried in the gen_ai group.

// genaiConsumed are the record keys the conversation section renders itself;
// the namespace-group renderer skips them to avoid duplication.
var genaiConsumed = []string{
	"gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions",
	"gen_ai.tool.call.arguments", "gen_ai.tool.call.result",
}

// genaiFlatPrefixes are the traceloop-style numbered attributes the
// conversation section consumes wholesale (gen_ai.prompt.3.tool_calls.0.name
// and friends would otherwise flood the gen_ai group with dozens of rows).
var genaiFlatPrefixes = []string{"gen_ai.prompt.", "gen_ai.completion."}

// markConversationConsumed flags every record key the conversation section
// rendered, so the namespace groups skip them.
func markConversationConsumed(rec map[string]any, rendered map[string]bool) {
	for _, key := range genaiConsumed {
		rendered[key] = true
	}
	for key := range rec {
		for _, prefix := range genaiFlatPrefixes {
			if strings.HasPrefix(key, prefix) {
				rendered[key] = true
			}
		}
	}
}

// addConversation renders a GenAI span's exchange as its own section and
// reports whether one was drawn. Both instrumentation conventions parse into
// the same message shape (catalog.GenAIInput/GenAIOutput). The last user
// prompt and the model's output arrive expanded — they are what the reader
// came for — while earlier turns and the system prompt collapse to previews.
func (v *inspectorView) addConversation(needle string) bool {
	rec := v.rec
	input := catalog.GenAIInput(rec)
	output := catalog.GenAIOutput(rec)
	system := catalog.GenAISystemInstructions(rec)
	toolName := catalog.Str(rec, "gen_ai.tool.name")
	toolArgs := catalog.Str(rec, "gen_ai.tool.call.arguments")
	toolResult := catalog.Str(rec, "gen_ai.tool.call.result")
	if len(input)+len(output) == 0 && system == "" && toolArgs == "" && toolResult == "" {
		return false
	}

	v.addLine(theme.Section("conversation"))
	addMsg := func(key, label string, style lipgloss.Style, text string, expand bool) {
		if text == "" || !fieldMatches(needle, label, text) {
			return
		}
		v.addRow(key, label, v.labelStyle(needle, label, style), valueView{
			lines:   wrapLines(text, v.vp.Width-6),
			compact: compactText(text),
			raw:     text,
			block:   expand,
		})
	}

	addMsg("gen_ai.system_instructions", "system", theme.Dim, system, false)

	lastUser := -1
	for i, m := range input {
		if m.Role == "user" {
			lastUser = i
		}
	}
	for i, m := range input {
		label := fmt.Sprintf("in[%d] · %s", i, m.Role)
		addMsg("gen_ai.input.messages", label, genaiRoleStyle(m.Role), genaiMessageText(m), i == lastUser)
	}
	for i, m := range output {
		label := fmt.Sprintf("out[%d] · %s", i, m.Role)
		addMsg("gen_ai.output.messages", label, genaiRoleStyle(m.Role), genaiMessageText(m), true)
	}

	if toolArgs != "" || toolResult != "" {
		if toolName == "" {
			toolName = "tool"
		}
		addMsg("gen_ai.tool.call.arguments", "⚙ "+toolName+" call", theme.GenAI, toolArgs, true)
		addMsg("gen_ai.tool.call.result", "⚙ "+toolName+" result", theme.GenAI, toolResult, true)
	}
	v.addLine("")
	return true
}

// genaiMessageText flattens a message's parts for display: text verbatim,
// reasoning marked, tool calls as name(arguments), tool results marked.
func genaiMessageText(m catalog.GenAIMessage) string {
	var out string
	for _, p := range m.Parts {
		var line string
		switch p.Type {
		case "text", "":
			line = p.Content
		case "reasoning":
			line = "﹝reasoning﹞ " + p.Content
		case "tool_call":
			name := p.Name
			if name == "" {
				name = "tool"
			}
			line = "→ " + name + " " + p.Content
		case "tool_call_response":
			line = "← " + p.Content
		default:
			line = "﹝" + p.Type + "﹞ " + p.Content
		}
		if line == "" {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += line
	}
	return out
}

// genaiRoleStyle colors a turn by its speaker.
func genaiRoleStyle(role string) lipgloss.Style {
	switch role {
	case "user":
		return theme.FactLabel
	case "assistant":
		return theme.StatusOK
	case "tool":
		return theme.GenAI
	case "system":
		// The flat convention carries the system prompt as message zero.
		return theme.Dim
	}
	return theme.Label
}

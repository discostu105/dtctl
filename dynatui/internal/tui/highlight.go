package tui

import (
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// Primitive in-place highlighting for free-form text blocks: gen_ai message
// bodies (markdown-ish prose, XML-tagged prompts) and stack traces. Rules
// style recognized tokens and change nothing else — same characters, same
// line breaks — so wrapping, search (raw values) and yank stay untouched, and
// no format detection is needed: a rule simply doesn't fire on content of
// another shape.

// Styles derived with tab conversion disabled: lipgloss rewrites tabs to
// spaces on Render by default, which would break the characters-unchanged
// invariant on tab-indented stack frames and fenced code (the wrap pipeline
// downstream may still normalize tabs, exactly as it does for plain text).
var (
	hlDim    = theme.Dim.TabWidth(lipgloss.NoTabConversion)
	hlHead   = theme.ProseHead.TabWidth(lipgloss.NoTabConversion)
	hlTag    = theme.ProseTag.TabWidth(lipgloss.NoTabConversion)
	hlCode   = theme.ProseCode.TabWidth(lipgloss.NoTabConversion)
	hlMarker = theme.ProseMarker.TabWidth(lipgloss.NoTabConversion)
	hlGenAI  = theme.GenAI.TabWidth(lipgloss.NoTabConversion)
	hlErr    = theme.Error.TabWidth(lipgloss.NoTabConversion)
	hlLoc    = theme.StackLoc.TabWidth(lipgloss.NoTabConversion)
)

// --- prose (gen_ai message bodies) -------------------------------------------

var (
	proseFenceRe   = regexp.MustCompile("^\\s*```")
	proseHeadingRe = regexp.MustCompile(`^#{1,6} `)
	proseQuoteRe   = regexp.MustCompile(`^\s*> ?`)
	proseBulletRe  = regexp.MustCompile(`^(\s*)([-*•]|\d{1,3}[.)])( +)`)
	// The part markers genaiMessageText emits: "→ tool args", "← result",
	// "﹝reasoning﹞ …".
	proseMarkerRe = regexp.MustCompile(`^(?:→ \S+|←|﹝[^﹞]*﹞)`)
	proseTagRe    = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9_.:-]*(?:\s[^<>]*)?/?>`)
	proseCodeRe   = regexp.MustCompile("`[^`]+`")
)

// highlightProse styles the structural tokens of a free-form text block —
// markdown headings, list markers, code fences and spans, XML-ish tags, and
// the conversation part markers — leaving every character in place.
func highlightProse(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	inFence := false
	for i, line := range lines {
		switch {
		case proseFenceRe.MatchString(line):
			inFence = !inFence
			lines[i] = hlDim.Render(line)
		case inFence:
			lines[i] = hlCode.Render(line)
		default:
			lines[i] = highlightProseLine(line)
		}
	}
	return strings.Join(lines, "\n")
}

func highlightProseLine(line string) string {
	if proseHeadingRe.MatchString(line) {
		return hlHead.Render(line)
	}
	if m := proseQuoteRe.FindString(line); m != "" {
		return hlDim.Render(m) + highlightProseInline(line[len(m):])
	}
	if m := proseBulletRe.FindStringSubmatch(line); m != nil {
		return m[1] + hlMarker.Render(m[2]) + m[3] + highlightProseInline(line[len(m[0]):])
	}
	if m := proseMarkerRe.FindString(line); m != "" {
		return hlGenAI.Render(m) + highlightProseInline(line[len(m):])
	}
	return highlightProseInline(line)
}

// highlightProseInline styles inline code spans and XML-ish tags; tags inside
// a code span stay code.
func highlightProseInline(s string) string {
	if !strings.ContainsAny(s, "`<") {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range proseCodeRe.FindAllStringIndex(s, -1) {
		b.WriteString(highlightProseTags(s[last:loc[0]]))
		b.WriteString(hlCode.Render(s[loc[0]:loc[1]]))
		last = loc[1]
	}
	b.WriteString(highlightProseTags(s[last:]))
	return b.String()
}

func highlightProseTags(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	return proseTagRe.ReplaceAllStringFunc(s, func(tag string) string {
		return hlTag.Render(tag)
	})
}

// --- stack traces --------------------------------------------------------------

var (
	// Whole-line noise: elided-frame markers, Go(routine) headers, the Python
	// traceback banner.
	stackNoiseRe = regexp.MustCompile(
		`^(?:\.\.\. \d+ (?:more|common frames omitted).*|goroutine \d+ \[.*|Traceback \(most recent call last\):|--- End of .*)$`)
	stackAtRe = regexp.MustCompile(`^\s*(at)\s+`)
	// Exception headers: "java.lang.FooException: msg", "ValueError: msg",
	// "Caused by: …", Go panics. The trailing (?::|$) keeps frame lines like
	// "ExceptionHandler.handle(…)" from matching.
	stackHeadRe = regexp.MustCompile(
		`^\s*(?:(?:Caused by|Suppressed):\s+)?(?:panic|fatal error|[\w.$]*(?:Exception|Error|Throwable)[\w$]*)(?::|$)`)
	// Source locations: "Foo.java:42", "/app/src/index.js:10:15",
	// "C:\src\Worker.cs:line 42" (the drive letter stays plain).
	stackLocRe   = regexp.MustCompile(`[\w$~/\\.-]+\.[A-Za-z]{1,5}(?::(?:line )?\d+)+`)
	stackPyLocRe = regexp.MustCompile(`"[^"]+", line \d+`)
	// The qualified function of a frame — "at" optional (OneAgent call stacks
	// carry bare frames): dim the package, keep Class.method loud.
	stackFuncRe = regexp.MustCompile(`^\s*(?:at\s+)?([A-Za-z_$][\w$.<>]*)\s*\(`)
	// Trailing build metadata: "~[spring-web.jar:5.3]", Go's "+0x20".
	stackMetaRe = regexp.MustCompile(`\s(?:~?\[[^\]]*\]|\+0x[0-9a-f]+)\s*$`)
)

// highlightStack styles a callstack in place, line by line: exception headers
// loud, source locations sky, frame plumbing (at, package prefixes, build
// metadata, elision markers) dim.
func highlightStack(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = highlightStackLine(line)
	}
	return strings.Join(lines, "\n")
}

// hlSpan is one styled byte range of a line.
type hlSpan struct {
	start, end int
	style      lipgloss.Style
}

func highlightStackLine(line string) string {
	if stackNoiseRe.MatchString(strings.TrimSpace(line)) {
		return hlDim.Render(line)
	}

	// Collect spans in priority order; a later rule never restyles an
	// already-claimed range (e.g. "jar:9.0" inside dimmed build metadata must
	// not light up as a location).
	var spans []hlSpan
	add := func(start, end int, style lipgloss.Style) {
		if start >= end {
			return
		}
		for _, sp := range spans {
			if start < sp.end && sp.start < end {
				return
			}
		}
		spans = append(spans, hlSpan{start: start, end: end, style: style})
	}

	if m := stackAtRe.FindStringSubmatchIndex(line); m != nil {
		add(m[2], m[3], hlDim)
	} else if m := stackHeadRe.FindStringIndex(line); m != nil {
		add(m[0], m[1], hlErr)
	}
	if m := stackMetaRe.FindStringIndex(line); m != nil {
		add(m[0], m[1], hlDim)
	}
	for _, m := range stackLocRe.FindAllStringIndex(line, -1) {
		add(m[0], m[1], hlLoc)
	}
	for _, m := range stackPyLocRe.FindAllStringIndex(line, -1) {
		add(m[0], m[1], hlLoc)
	}
	if m := stackFuncRe.FindStringSubmatchIndex(line); m != nil {
		if cut := packageCut(line[m[2]:m[3]]); cut > 0 {
			add(m[2], m[2]+cut, hlDim)
		}
	}

	if len(spans) == 0 {
		return line
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(line[last:sp.start])
		b.WriteString(sp.style.Render(line[sp.start:sp.end]))
		last = sp.end
	}
	b.WriteString(line[last:])
	return b.String()
}

// packageCut returns the byte length of the package prefix to dim in a
// qualified function token, keeping the last two dot-segments loud
// ("org.foo.bar.Class.method" → len("org.foo.bar.")).
func packageCut(tok string) int {
	parts := strings.Split(tok, ".")
	if len(parts) <= 2 {
		return 0
	}
	cut := 0
	for _, p := range parts[:len(parts)-2] {
		cut += len(p) + 1
	}
	return cut
}

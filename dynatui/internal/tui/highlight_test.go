package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// forceColor enables a color profile for the test — without a TTY lipgloss
// renders plain, which would make highlighting assertions vacuous.
func forceColor(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

// The core invariant: highlighting styles tokens in place and never changes
// the characters — stripping the escapes must reproduce the input exactly.
func TestHighlightKeepsTextIntact(t *testing.T) {
	forceColor(t)
	samples := []string{
		"",
		"plain prose, nothing to see",
		"# Title\n\nUse `dtctl` with <ctx name=\"prod\"> tags.\n- one\n- two\n```json\n{\"a\": 1}\n```\ntail",
		"unclosed fence\n```\ncode until the end",
		"a < b and b > c stay plain",
		"→ Read {\"path\": \"/tmp\"}\n← ok\n﹝reasoning﹞ hmm",
	}
	for _, s := range samples {
		if got := ansi.Strip(highlightProse(s)); got != s {
			t.Errorf("highlightProse mutated text:\n in: %q\nout: %q", s, got)
		}
	}

	stacks := []string{
		javaStack,
		pyStack,
		goStack,
		"just a message, no frames at all",
	}
	for _, s := range stacks {
		if got := ansi.Strip(highlightStack(s)); got != s {
			t.Errorf("highlightStack mutated text:\n in: %q\nout: %q", s, got)
		}
	}
}

func TestHighlightProseStructure(t *testing.T) {
	forceColor(t)
	got := highlightProse("# Summary\n<instructions>\nRun `dtctl doctor` first.\n- check ctx\n</instructions>")
	for _, want := range []string{
		hlHead.Render("# Summary"),
		hlTag.Render("<instructions>"),
		hlTag.Render("</instructions>"),
		hlCode.Render("`dtctl doctor`"),
		hlMarker.Render("-"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing styled token %q in:\n%q", want, got)
		}
	}
}

func TestHighlightProseFences(t *testing.T) {
	forceColor(t)
	got := highlightProse("before\n```go\nfmt.Println(1)\n```\nafter <tag>")
	for _, want := range []string{
		hlDim.Render("```go"),
		hlCode.Render("fmt.Println(1)"),
		hlTag.Render("<tag>"), // prose again after the closing fence
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing styled token %q in:\n%q", want, got)
		}
	}
	// Inside a fence no prose rules fire — the line is one code token.
	inFence := highlightProse("```\n- not a bullet\n```")
	if !strings.Contains(inFence, hlCode.Render("- not a bullet")) {
		t.Errorf("fenced line must render as code, got:\n%q", inFence)
	}
}

func TestHighlightProseConversationMarkers(t *testing.T) {
	forceColor(t)
	got := highlightProse("→ Read {\"path\": \"/tmp\"}\n﹝reasoning﹞ thinking…")
	for _, want := range []string{
		hlGenAI.Render("→ Read"),
		hlGenAI.Render("﹝reasoning﹞"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing styled marker %q in:\n%q", want, got)
		}
	}
}

const javaStack = "java.lang.IllegalStateException: connection lost\n" +
	"\tat org.apache.juli.logging.DirectJDKLog.log(DirectJDKLog.java:175) ~[tomcat.jar:9.0]\n" +
	"\tat com.example.Service.run(Service.java:42)\n" +
	"\t... 23 more\n" +
	"Caused by: java.net.SocketTimeoutException: connect timed out"

const pyStack = "Traceback (most recent call last):\n" +
	"  File \"/app/main.py\", line 10, in <module>\n" +
	"    do_thing()\n" +
	"ValueError: bad value"

const goStack = "goroutine 1 [running]:\n" +
	"main.main()\n" +
	"\t/app/main.go:10 +0x20"

func TestHighlightStackJava(t *testing.T) {
	forceColor(t)
	got := highlightStack(javaStack)
	for _, want := range []string{
		hlErr.Render("java.lang.IllegalStateException:"),
		hlErr.Render("Caused by: java.net.SocketTimeoutException:"),
		hlDim.Render("at"),
		hlDim.Render("org.apache.juli.logging."), // package dim, Class.method loud
		hlLoc.Render("DirectJDKLog.java:175"),
		hlLoc.Render("Service.java:42"),
		hlDim.Render(" ~[tomcat.jar:9.0]"),
		hlDim.Render("\t... 23 more"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing styled token %q in:\n%q", want, got)
		}
	}
	// The version inside the dimmed build metadata must not restyle as a
	// location.
	if strings.Contains(got, hlLoc.Render("tomcat.jar:9")) {
		t.Errorf("build metadata wrongly styled as location:\n%q", got)
	}
}

func TestHighlightStackPythonAndGo(t *testing.T) {
	forceColor(t)
	got := highlightStack(pyStack)
	for _, want := range []string{
		hlDim.Render("Traceback (most recent call last):"),
		hlLoc.Render(`"/app/main.py", line 10`),
		hlErr.Render("ValueError:"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing styled token %q in:\n%q", want, got)
		}
	}

	got = highlightStack(goStack)
	for _, want := range []string{
		hlDim.Render("goroutine 1 [running]:"),
		hlLoc.Render("/app/main.go:10"),
		hlDim.Render(" +0x20"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing styled token %q in:\n%q", want, got)
		}
	}
}

func TestHighlightStackDotNet(t *testing.T) {
	forceColor(t)
	line := "   at MyApp.Services.Worker.Run(CancellationToken token) in /src/Worker.cs:line 42"
	got := highlightStackLine(line)
	for _, want := range []string{
		hlDim.Render("at"),
		hlDim.Render("MyApp.Services."),
		hlLoc.Render("/src/Worker.cs:line 42"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing styled token %q in:\n%q", want, got)
		}
	}
}

func TestPackageCut(t *testing.T) {
	cases := map[string]int{
		"org.foo.bar.Class.method": len("org.foo.bar."),
		"Class.method":             0,
		"do_thing":                 0,
		"main.":                    0, // Go's "main.(*Server)" leaves a bare prefix
	}
	for tok, want := range cases {
		if got := packageCut(tok); got != want {
			t.Errorf("packageCut(%q) = %d, want %d", tok, got, want)
		}
	}
}

package tui

import (
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
)

// inputCursorMode is applied to every text input and textarea the TUI
// constructs. Tests set CursorStatic: with a blinking cursor, the command
// returned on focus and on every keystroke blocks its goroutine for the
// 530ms blink interval, which the synchronous test harness would pay per
// simulated key press.
var inputCursorMode = cursor.CursorBlink

// newTextInput is how the TUI constructs text inputs — do not call
// textinput.New directly, or the test cursor mode won't apply.
func newTextInput() textinput.Model {
	ti := textinput.New()
	ti.Cursor.SetMode(inputCursorMode)
	return ti
}

// newTextArea is the textarea counterpart of newTextInput.
func newTextArea() textarea.Model {
	ta := textarea.New()
	ta.Cursor.SetMode(inputCursorMode)
	return ta
}

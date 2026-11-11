// Package desktop locks onto the focused text field of another application,
// types text into it and owns the global start/stop hotkey.
//
// Every platform implements Start, SetHotkey, Acquire, SetAutostart and Close
// in a build-tagged file.
package desktop

import (
	"errors"
	"image"

	"xvoice/internal/config"
)

var ErrUnsupported = errors.New("当前平台尚未实现外部输入框写入")

// Target is one external text field locked for a dictation session. It ends
// (Done closes) as soon as focus, the foreground window or the keyboard says
// the user moved on; Type after that is refused, never misdirected.
type Target interface {
	// Type presses Backspace erase times, then types text.
	Type(erase int, text string) error
	// Anchor is the caret rectangle in screen pixels, or the field's bounds
	// when the caret cannot be located; ok is false if neither is known.
	Anchor() (r image.Rectangle, ok bool)
	Done() <-chan struct{}
	Reason() string
	Release()
}

// Hotkeys are the start/stop chords offered in settings. A config file may
// hold any other chord of the same form, such as "ctrl+shift+f12".
var Hotkeys = []config.Choice{
	{Value: "", Name: "Ctrl + Alt + 空格"},
	{Value: "ctrl+alt+h", Name: "Ctrl + Alt + H"},
	{Value: "ctrl+shift+h", Name: "Ctrl + Shift + H"},
	{Value: "win+alt+h", Name: "Win + Alt + H"},
	{Value: "f9", Name: "F9"},
}

// HotkeyName is how a hotkey value is shown to the user.
func HotkeyName(value string) string {
	for _, h := range Hotkeys {
		if h.Value == value {
			return h.Name
		}
	}
	return value
}

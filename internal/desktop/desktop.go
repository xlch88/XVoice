// Package desktop locks onto the focused text field of another application,
// types text into it and owns the global start/stop hotkey.
//
// Every platform implements Start, SetHotkey, CaptureHotkey, Acquire,
// SetAutostart and Close
// in a build-tagged file.
package desktop

import (
	"errors"
	"image"
	"strings"
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

// HotkeyName is how a hotkey value such as "ctrl+alt+space" is shown to the
// user. The empty value is the default hotkey.
func HotkeyName(value string) string {
	if value == "" {
		value = "ctrl+alt+space"
	}
	parts := strings.Split(value, "+")
	for i, part := range parts {
		switch part {
		case "ctrl", "alt", "shift", "win":
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		case "space":
			parts[i] = "空格"
		default:
			parts[i] = strings.ToUpper(part)
		}
	}
	return strings.Join(parts, " + ")
}

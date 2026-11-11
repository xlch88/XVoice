package desktop

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	setWindowsHook = user32.NewProc("SetWindowsHookExW")
	callNextHook   = user32.NewProc("CallNextHookEx")
	unhookWindows  = user32.NewProc("UnhookWindowsHookEx")
	setWinEvent    = user32.NewProc("SetWinEventHook")
	unhookWinEvent = user32.NewProc("UnhookWinEvent")
	getMessage     = user32.NewProc("GetMessageW")
)

const (
	vkShift = 0x10
	vkCtrl  = 0x11
	vkAlt   = 0x12
	vkLWin  = 0x5b
	vkRWin  = 0x5c
	vkMask  = 0xe8 // Assigned to no key; see sendMask.
)

// The hotkey is detected inside the low-level hook rather than RegisterHotKey,
// so the same hook can tell "user pressed the hotkey" from "user typed while
// dictating" and swallow the hotkey's key before the target app sees it.
//
// chord is the hotkey for the hook thread: its key code in the low byte and
// one bit per required modifier.
var chord atomic.Uint32

const (
	chordCtrl = 1 << (8 + iota)
	chordAlt
	chordShift
	chordWin
)

// SetHotkey installs a chord such as "ctrl+alt+space", "win+alt+h" or "f9".
// A value it cannot read selects the default chord.
func SetHotkey(value string) {
	c, ok := parseHotkey(value)
	if !ok {
		c, _ = parseHotkey("ctrl+alt+space")
	}
	chord.Store(c)
}

func parseHotkey(value string) (c uint32, ok bool) {
	if value == "" {
		value = "ctrl+alt+space"
	}
	for _, part := range strings.Split(strings.ToLower(value), "+") {
		switch {
		case part == "ctrl":
			c |= chordCtrl
		case part == "alt":
			c |= chordAlt
		case part == "shift":
			c |= chordShift
		case part == "win":
			c |= chordWin
		case c&0xff != 0:
			return 0, false // A second key.
		case part == "space":
			c |= 0x20
		case len(part) == 1 && (unicode.IsLower(rune(part[0])) || unicode.IsDigit(rune(part[0]))):
			c |= uint32(unicode.ToUpper(rune(part[0]))) // Letter and digit key codes are their ASCII.
		case strings.HasPrefix(part, "f"):
			n, err := strconv.Atoi(part[1:])
			if err != nil || n < 1 || n > 24 {
				return 0, false
			}
			c |= uint32(0x70 + n - 1)
		default:
			return 0, false
		}
	}
	return c, c&0xff != 0
}

type keyboardEvent struct {
	VK, Scan, Flags, Time uint32
	Extra                 uintptr
}

// Hook state lives on the hook thread only.
var (
	held         [256]bool
	pendingStart bool   // Hotkey pressed while idle; fires once every key is up.
	interrupted  bool   // A key stopped dictation; ignore the hotkey until all keys are up.
	swallowed    uint32 // Key whose key-down was eaten, so its key-up is too.
	onHotkeyFunc func()
)

// modifier folds left/right variants into the generic virtual key.
func modifier(vk uint32) uint32 {
	switch vk {
	case 0xa0, 0xa1:
		return vkShift
	case 0xa2, 0xa3:
		return vkCtrl
	case 0xa4, 0xa5:
		return vkAlt
	}
	return vk
}

// maskTag marks sendMask's events so the hook lets them through untouched.
const maskTag uintptr = 0x584d << 48

// sendMask taps a key no keyboard has. Because the hotkey's own key is
// swallowed, Windows would otherwise see the modifiers pressed and released
// alone and react to that: Alt opens the menu bar, Win the Start menu,
// Ctrl+Shift or Alt+Shift switches the input method. One tap of any other key
// prevents it. Ctrl+Alt alone triggers nothing and needs no mask.
func sendMask() {
	input := [2]keyInput{{Kind: 1, VK: vkMask, Extra: maskTag}, {Kind: 1, VK: vkMask, Flags: 2, Extra: maskTag}}
	sendInput.Call(2, uintptr(unsafe.Pointer(&input[0])), unsafe.Sizeof(input[0]))
}

var keyboardCallback = syscall.NewCallback(func(code, message uintptr, event *keyboardEvent) uintptr {
	if int32(code) < 0 || event.Extra == maskTag {
		return next(code, message, event)
	}
	down := message == 0x100 || message == 0x104 // WM_KEYDOWN, WM_SYSKEYDOWN
	if event.Extra&^0xffffffffffff == inputTag {
		// Our own SendInput: drop key-downs that belong to a stopped target.
		if t := current(); down && (t == nil || uint64(event.Extra&0xffffffffffff) != t.token) {
			return 1
		}
		return next(code, message, event)
	}
	vk := modifier(event.VK)
	if vk > 255 {
		return next(code, message, event)
	}
	wasHeld := held[vk]
	held[vk] = down
	if !down {
		swallow := vk == swallowed
		if swallow {
			swallowed = 0
		}
		if !anyHeld() {
			interrupted = false
			if pendingStart {
				pendingStart = false
				// Fire only after release so the modifiers cannot combine with typed text.
				go onHotkeyFunc()
			}
		}
		if swallow {
			return 1
		}
		return next(code, message, event)
	}
	if t := current(); t != nil {
		stopIf(t, "检测到键盘输入")
		interrupted = true
	}
	c := chord.Load()
	if vk == c&0xff &&
		held[vkCtrl] == (c&chordCtrl != 0) && held[vkAlt] == (c&chordAlt != 0) &&
		held[vkShift] == (c&chordShift != 0) && (held[vkLWin] || held[vkRWin]) == (c&chordWin != 0) {
		if !wasHeld {
			pendingStart = !interrupted
			if c&(chordWin|chordShift) != 0 || c&(chordCtrl|chordAlt) == chordAlt {
				sendMask()
			}
		}
		swallowed = vk
		return 1
	}
	if vk != vkCtrl && vk != vkAlt && vk != vkShift && vk != vkLWin && vk != vkRWin {
		pendingStart = false
	}
	return next(code, message, event)
})

func anyHeld() bool {
	for _, h := range held {
		if h {
			return true
		}
	}
	return false
}

func next(code, message uintptr, event *keyboardEvent) uintptr {
	r, _, _ := callNextHook.Call(0, code, message, uintptr(unsafe.Pointer(event)))
	return r
}

// The event only prompts a look at the real foreground window. Its own window
// argument is not trustworthy: apps report helper windows (a UWP CoreWindow
// under its frame, accessibility windows created on the first UI Automation
// query), and events queued before the target was locked arrive late.
var foregroundCallback = syscall.NewCallback(func(_ uintptr, _ uint32, _ uintptr, _, _ int32, _, _ uint32) uintptr {
	t := current()
	if t == nil {
		return 0
	}
	foreground, _, _ := getForeground.Call()
	if foreground == 0 || foreground == t.window {
		return 0 // Zero is a transition in progress; the next event settles it.
	}
	var process uint32
	getThreadProcess.Call(foreground, uintptr(unsafe.Pointer(&process)))
	if process != windows.GetCurrentProcessId() {
		stopIf(t, "窗口已切换")
	}
	return 0
})

// runHooks installs the hooks and pumps messages until WM_QUIT.
func runHooks(onHotkey func(), ready chan<- error) {
	onHotkeyFunc = onHotkey
	module, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	keyboard, _, keyboardErr := setWindowsHook.Call(13, keyboardCallback, module, 0) // WH_KEYBOARD_LL
	// EVENT_SYSTEM_FOREGROUND, WINEVENT_OUTOFCONTEXT
	foreground, _, foregroundErr := setWinEvent.Call(3, 3, 0, foregroundCallback, 0, 0, 0)
	defer func() {
		if keyboard != 0 {
			unhookWindows.Call(keyboard)
		}
		if foreground != 0 {
			unhookWinEvent.Call(foreground)
		}
	}()
	if keyboard == 0 || foreground == 0 {
		ready <- fmt.Errorf("安装全局钩子失败：键盘 %v，窗口 %v", keyboardErr, foregroundErr)
		return
	}
	ready <- nil
	var msg [48]byte // MSG
	for {
		if r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0); int32(r) <= 0 {
			return
		}
	}
}

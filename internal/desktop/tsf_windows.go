package desktop

import (
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/xlch88/XVoice/internal/tip"
)

// The XVoice text service (tip/xvoice_tip.cpp) is a TSF speech TIP that TSF
// loads into applications next to the keyboard IME. On every thread that gets
// a text focus it keeps a message-only window; commands sent to that window
// run on the application's UI thread, where the service edits the field as a
// composition. It is the only way text is written.

var (
	findWindowEx       = user32.NewProc("FindWindowExW")
	sendMessageTimeout = user32.NewProc("SendMessageTimeoutW")
	tipClass           = windows.StringToUTF16Ptr(tip.WindowClass)
)

// Commands of the text service window, see tip/xvoice_tip.cpp.
const (
	tipQuery   = 1 // -> generation<<2 | state (1 editable, 2 not writable, 0 no text focus)
	tipCompose = 2 // show text as the composition
	tipCommit  = 3 // replace the composition with text, if any, and end it
)

type guiThreadInfo struct {
	Size, Flags                                         uint32
	Active, Focus, Capture, MenuOwner, MoveSize, CaretW uintptr
	Caret                                               windows.Rect
}

func threadInfo(window uintptr) (guiThreadInfo, bool) {
	thread, _, _ := getThreadProcess.Call(window, 0)
	info := guiThreadInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	r, _, _ := getGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&info)))
	return info, r != 0
}

// findTip returns the text service window on the thread that owns the
// keyboard focus of window, or 0.
func findTip(window uintptr) uintptr {
	owner := window
	if info, ok := threadInfo(window); ok && info.Focus != 0 {
		owner = info.Focus // Another thread may have attached its input.
	}
	thread, _, _ := getThreadProcess.Call(owner, 0)
	var tip uintptr
	for {
		tip, _, _ = findWindowEx.Call(^uintptr(2), tip, uintptr(unsafe.Pointer(tipClass)), 0) // HWND_MESSAGE
		if tip == 0 {
			return 0
		}
		if t, _, _ := getThreadProcess.Call(tip, 0); t == thread {
			return tip
		}
	}
}

// tipSend delivers a command and returns its answer; ok is false when the
// application did not answer in time.
func tipSend(tip, command uintptr, text string) (answer uintptr, ok bool) {
	chars := utf16.Encode([]rune(text))
	data := struct {
		Command uintptr
		Size    uint32
		Text    unsafe.Pointer
	}{Command: command, Size: uint32(len(chars) * 2)}
	if len(chars) > 0 {
		data.Text = unsafe.Pointer(&chars[0])
	}
	// WM_COPYDATA; SMTO_BLOCK | SMTO_ABORTIFHUNG
	r, _, _ := sendMessageTimeout.Call(tip, 0x4a, 0, uintptr(unsafe.Pointer(&data)), 0x1|0x2, 500, uintptr(unsafe.Pointer(&answer)))
	return answer, r != 0
}

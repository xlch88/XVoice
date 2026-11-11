package desktop

import (
	"errors"
	"image"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"xvoice/internal/winapi"
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	getForeground     = user32.NewProc("GetForegroundWindow")
	getThreadProcess  = user32.NewProc("GetWindowThreadProcessId")
	getGUIThreadInfo  = user32.NewProc("GetGUIThreadInfo")
	clientToScreen    = user32.NewProc("ClientToScreen")
	sendInput         = user32.NewProc("SendInput")
	postThreadMessage = user32.NewProc("PostThreadMessageW")
)

type target struct {
	token    uint64
	id       []int32
	window   uintptr
	anchor   image.Rectangle
	anchored bool
	done     chan struct{}
	reason   string // Written once under mu before done closes.
}

// All UI Automation calls run on one MTA thread fed through jobs. The active
// target and its generation are shared with hook callbacks, which only stop.
var (
	jobs       = make(chan func())
	quit       chan struct{}
	finished   sync.WaitGroup
	mu         sync.Mutex
	active     *target
	generation atomic.Uint64
	hookThread uint32
)

func current() *target {
	mu.Lock()
	defer mu.Unlock()
	return active
}

// stopIf ends t if it is still the active target; later stops are no-ops.
func stopIf(t *target, reason string) {
	mu.Lock()
	defer mu.Unlock()
	if t == nil || active != t {
		return
	}
	active = nil
	generation.Add(1)
	t.reason = reason
	close(t.done)
}

func stopActive(reason string) { stopIf(current(), reason) }

// do runs f on the UIA worker and waits for it.
func do(f func()) {
	done := make(chan struct{})
	jobs <- func() {
		defer close(done)
		f()
	}
	<-done
}

// Start initialises UI Automation and the global keyboard hook. onHotkey runs
// on its own goroutine after the hotkey chord is fully released.
func Start(hotkey string, onHotkey func()) error {
	SetHotkey(hotkey)
	quit = make(chan struct{})
	ready := make(chan error, 2)
	finished.Add(2)
	go func() {
		defer finished.Done()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := winapi.InitMTA(); err != nil {
			ready <- err
			return
		}
		defer winapi.Uninit()
		if err := uiaInit(); err != nil {
			ready <- err
			return
		}
		defer uiaClose()
		ready <- nil
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				stopActive("已退出")
				return
			case job := <-jobs:
				job()
			case <-ticker.C:
				if t := current(); t != nil && !stillFocused(t) {
					stopIf(t, "输入框已切换")
				}
			}
		}
	}()
	go func() {
		defer finished.Done()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hookThread = windows.GetCurrentThreadId()
		runHooks(onHotkey, ready)
	}()
	var failed error
	for range 2 {
		if err := <-ready; err != nil && failed == nil {
			failed = err
		}
	}
	if failed != nil {
		Close()
	}
	return failed
}

func Close() {
	if quit == nil {
		return
	}
	close(quit)
	if hookThread != 0 {
		postThreadMessage.Call(uintptr(hookThread), 0x12, 0, 0) // WM_QUIT
	}
	finished.Wait()
	quit = nil
}

func stillFocused(t *target) bool {
	if w, _, _ := getForeground.Call(); w != t.window {
		return false
	}
	var focused *winapi.Object
	if winapi.Failed(engine.Call(autoFocused, uintptr(unsafe.Pointer(&focused)))) || focused == nil {
		return false
	}
	defer focused.Release()
	return slices.Equal(editorID(focused), t.id)
}

// Acquire locks the currently focused editable field of another process.
func Acquire() (Target, error) {
	var t *target
	var err error
	do(func() {
		var focused *winapi.Object
		if winapi.Failed(engine.Call(autoFocused, uintptr(unsafe.Pointer(&focused)))) || focused == nil {
			err = errors.New("无法取得当前焦点")
			return
		}
		defer focused.Release()
		var pid uint32
		focused.Call(elemProcessID, uintptr(unsafe.Pointer(&pid)))
		if pid == windows.GetCurrentProcessId() {
			err = errors.New("请先点击其他应用的输入框")
			return
		}
		editor := findEditor(focused)
		if editor == nil {
			err = errors.New("当前焦点不是可编辑的输入框")
			return
		}
		defer editor.Release()
		window, _, _ := getForeground.Call()
		t = &target{id: runtimeID(editor), window: window, done: make(chan struct{})}
		if len(t.id) == 0 {
			t, err = nil, errors.New("无法识别当前输入框")
			return
		}
		t.anchor, t.anchored = systemCaret(window)
		if !t.anchored {
			t.anchor, t.anchored = caretRect(editor)
		}
	})
	if t == nil {
		return nil, err
	}
	stopActive("已切换到新的输入")
	mu.Lock()
	t.token = generation.Add(1)
	active = t
	mu.Unlock()
	return t, nil
}

// systemCaret covers classic Win32 edits that maintain a system caret.
func systemCaret(window uintptr) (image.Rectangle, bool) {
	thread, _, _ := getThreadProcess.Call(window, 0)
	info := struct {
		Size, Flags                                         uint32
		Active, Focus, Capture, MenuOwner, MoveSize, CaretW uintptr
		Caret                                               windows.Rect
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, _ := getGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&info))); r == 0 || info.CaretW == 0 || info.Caret.Bottom <= info.Caret.Top {
		return image.Rectangle{}, false
	}
	points := [2]struct{ X, Y int32 }{{info.Caret.Left, info.Caret.Top}, {info.Caret.Right, info.Caret.Bottom}}
	for i := range points {
		clientToScreen.Call(info.CaretW, uintptr(unsafe.Pointer(&points[i])))
	}
	return image.Rect(int(points[0].X), int(points[0].Y), int(points[1].X)+1, int(points[1].Y)), true
}

func (t *target) Done() <-chan struct{} { return t.done }

func (t *target) Reason() string {
	mu.Lock()
	defer mu.Unlock()
	return t.reason
}

func (t *target) Anchor() (image.Rectangle, bool) { return t.anchor, t.anchored }

func (t *target) Release() { stopIf(t, "已停止") }

// SendInput events carry tag|token so the keyboard hook can drop any that are
// still queued after this target stopped.
const inputTag uintptr = 0x5846 << 48

type keyInput struct {
	Kind        uint32
	_           uint32
	VK, Scan    uint16
	Flags, Time uint32
	Extra       uintptr
	_           [8]byte
}

func (t *target) Type(erase int, text string) error {
	var err error
	do(func() {
		if current() != t || !stillFocused(t) {
			stopIf(t, "输入框已切换")
			err = errors.New("输入框已失去焦点")
			return
		}
		chars := utf16.Encode([]rune(text))
		if len(chars) == 0 && erase == 0 {
			return
		}
		input := make([]keyInput, 0, (len(chars)+erase)*2)
		for range erase {
			input = append(input,
				keyInput{Kind: 1, VK: 0x08, Extra: inputTag | uintptr(t.token)},
				keyInput{Kind: 1, VK: 0x08, Flags: 2, Extra: inputTag | uintptr(t.token)})
		}
		for _, c := range chars {
			// KEYEVENTF_UNICODE, then | KEYEVENTF_KEYUP
			input = append(input,
				keyInput{Kind: 1, Scan: c, Flags: 4, Extra: inputTag | uintptr(t.token)},
				keyInput{Kind: 1, Scan: c, Flags: 6, Extra: inputTag | uintptr(t.token)})
		}
		if n, _, _ := sendInput.Call(uintptr(len(input)), uintptr(unsafe.Pointer(&input[0])), unsafe.Sizeof(input[0])); int(n) != len(input) {
			stopIf(t, "文字输入失败")
			err = errors.New("SendInput 被拒绝：目标程序可能以管理员身份运行")
		}
	})
	return err
}

package desktop

import (
	"errors"
	"image"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/xlch88/XVoice/internal/tip"
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	getForeground     = user32.NewProc("GetForegroundWindow")
	getThreadProcess  = user32.NewProc("GetWindowThreadProcessId")
	getGUIThreadInfo  = user32.NewProc("GetGUIThreadInfo")
	clientToScreen    = user32.NewProc("ClientToScreen")
	postThreadMessage = user32.NewProc("PostThreadMessageW")
)

// target is a text field reached through the XVoice text service, which
// edits it as a composition on the application's UI thread.
type target struct {
	window   uintptr
	tip      uintptr // Text service window on the thread that owns the focus.
	state    uintptr // Its QUERY answer when acquired; any change means the focus moved.
	composed []rune  // The composition so far.
	anchor   image.Rectangle
	anchored bool
	done     chan struct{}
	reason   string // Written once under mu before done closes.
}

// Text service calls run on one worker fed through jobs, which also polls the
// active target. The active target is shared with hook callbacks, which only
// stop it.
var (
	jobs       = make(chan func())
	quit       chan struct{}
	finished   sync.WaitGroup
	mu         sync.Mutex
	active     *target
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
	t.reason = reason
	close(t.done)
}

func stopActive(reason string) { stopIf(current(), reason) }

// do runs f on the worker and waits for it.
func do(f func()) {
	done := make(chan struct{})
	jobs <- func() {
		defer close(done)
		f()
	}
	<-done
}

// Start runs the worker and the global keyboard hook. onHotkey runs on its own
// goroutine after the hotkey chord is fully released.
func Start(hotkey string, onHotkey func()) error {
	SetHotkey(hotkey)
	quit = make(chan struct{})
	ready := make(chan error, 1)
	finished.Add(2)
	go func() {
		defer finished.Done()
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
	err := <-ready
	if err != nil {
		Close()
	}
	return err
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

// stillFocused reports whether t's window is in front and the text service
// still reports the same focus. A busy application that does not answer in
// time keeps its target.
func stillFocused(t *target) bool {
	if w, _, _ := getForeground.Call(); w != t.window {
		return false
	}
	state, ok := tipSend(t.tip, tipQuery, "")
	return !ok || state == t.state
}

// Acquire locks the focused text field of another process. Only the text
// service can tell a text field from anything else and write into it, so
// without it nothing is acquired.
func Acquire() (Target, error) {
	var t *target
	var err error
	do(func() {
		window, _, _ := getForeground.Call()
		var pid uint32
		getThreadProcess.Call(window, uintptr(unsafe.Pointer(&pid)))
		if window == 0 || pid == windows.GetCurrentProcessId() {
			err = errors.New("请先点击其他应用的输入框")
			return
		}
		service := findTip(window)
		if service == 0 {
			if !tip.Check().Installed {
				err = errors.New("请先在设置里安装输入法服务")
			} else {
				// The service creates its window on a thread's first text focus.
				err = errors.New("当前焦点不是输入框")
			}
			return
		}
		state, ok := tipSend(service, tipQuery, "")
		switch {
		case !ok:
			err = errors.New("目标程序没有响应")
		case state&3 == 0:
			err = errors.New("当前焦点不是输入框")
		case state&3 == 2:
			err = errors.New("当前输入框不能输入")
		default:
			t = &target{window: window, tip: service, state: state, done: make(chan struct{})}
			t.anchor, t.anchored = systemCaret(window)
		}
	})
	if t == nil {
		return nil, err
	}
	stopActive("已切换到新的输入")
	mu.Lock()
	active = t
	mu.Unlock()
	return t, nil
}

// systemCaret is where the application says its caret is, for placing the bar.
func systemCaret(window uintptr) (image.Rectangle, bool) {
	info, ok := threadInfo(window)
	if !ok || info.CaretW == 0 || info.Caret.Bottom <= info.Caret.Top {
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

// Release ends the target and commits the composition so that it stays in
// the field.
func (t *target) Release() {
	do(func() { tipSend(t.tip, tipCommit, "") })
	stopIf(t, "已停止")
}

// Type replaces the last erase characters of the composition with text.
func (t *target) Type(erase int, text string) error {
	var err error
	do(func() {
		if current() != t || !stillFocused(t) {
			stopIf(t, "输入框已切换")
			err = errors.New("输入框已失去焦点")
			return
		}
		if erase == 0 && text == "" {
			return
		}
		t.composed = append(t.composed[:max(0, len(t.composed)-erase)], []rune(text)...)
		if queued, _ := tipSend(t.tip, tipCompose, string(t.composed)); queued != 1 {
			stopIf(t, "文字输入失败")
			err = errors.New("输入法服务没有响应")
		}
	})
	return err
}

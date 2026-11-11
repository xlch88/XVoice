package ui

import (
	"context"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/xlch88/XVoice/internal/config"
	"github.com/xlch88/XVoice/internal/desktop"
)

// The settings window: the form from settingsFields on the left, rendered by
// one loop, and a recognition test on the right.

const (
	idTest  = 90
	idAbout = 91
	idField = 100 // Plus the field's index.

	settingsStyle = wsCaption | wsSysMenu | wsMinimizeBox
)

// row is the controls of one field.
type row struct {
	heading, label, combo uintptr
	shown                 []config.Choice // What the combo lists, by position.
}

var (
	settingsWnd   uintptr
	settingsDPI   int
	settingsFonts struct{ text, bold uintptr }
	form          []field
	rows          []row
	footnote      uintptr
	aboutButton   uintptr
	divider       uintptr
	testHeading   uintptr
	testButton    uintptr
	testStatus    uintptr
	testLevel     uintptr
	testEdit      uintptr
	testCancel    context.CancelFunc

	// Written by background goroutines, read on the UI thread after a post.
	shared struct {
		sync.Mutex
		choices map[int][]config.Choice
		text    string
		hotkey  string
		level   float64
		status  string
		running bool
	}
)

const testHint = "对着麦克风说话。结果只显示在下面，不会输入到其他应用。"

// captions remembers what setCaption last wrote, so the 25 updates a second
// during a test only repaint a control when its text really changed.
var captions = map[uintptr]string{}

func setCaption(hwnd uintptr, text string) {
	if last, ok := captions[hwnd]; !ok || last != text {
		captions[hwnd] = text
		setWindowText.Call(hwnd, uintptr(unsafe.Pointer(str(text))))
	}
}

func child(class, text string, style, exStyle uintptr, id int) uintptr {
	hwnd, _, _ := createWindow.Call(exStyle, uintptr(unsafe.Pointer(str(class))), uintptr(unsafe.Pointer(str(text))), wsChild|wsVisible|style,
		0, 0, 0, 0, settingsWnd, uintptr(id), instance, 0)
	return hwnd
}

func createSettings() {
	form = settingsFields(opts.Devices)
	shared.choices = map[int][]config.Choice{}
	settingsWnd, _, _ = createWindow.Call(wsExCtrlPar, uintptr(unsafe.Pointer(str("XVoiceSettings"))), uintptr(unsafe.Pointer(str("XVoice 设置"))), settingsStyle,
		0x80000000, 0x80000000, 0, 0, 0, 0, instance, 0) // CW_USEDEFAULT
	rows = make([]row, len(form))
	for i, f := range form {
		if f.section != "" {
			rows[i].heading = child("STATIC", f.section, 0, 0, 0)
		}
		rows[i].label = child("STATIC", f.label, 0, 0, 0)
		if f.hotkey {
			rows[i].combo = child("BUTTON", desktop.HotkeyName(*f.value(&cfg)), wsTabStop, 0, idField+i)
			continue
		}
		rows[i].combo = child("COMBOBOX", "", 0x3|wsVScroll|wsTabStop, 0, idField+i) // CBS_DROPDOWNLIST
		send(rows[i].combo, 0x1701, 16, 0)                                           // CB_SETMINVISIBLE: rows listed before scrolling
		fill(i, f.choices)
	}
	footnote = child("STATIC", "更改立即保存，从下一次听写开始生效。", 0, 0, 0)
	aboutButton = child("BUTTON", "关于 "+opts.Version, wsTabStop, 0, idAbout)
	divider = child("STATIC", "", 0x11, 0, 0) // SS_ETCHEDVERT
	testHeading = child("STATIC", "测试", 0, 0, 0)
	testButton = child("BUTTON", "开始测试", wsTabStop, 0, idTest)
	testStatus = child("STATIC", testHint, 0, 0, 0)
	testLevel = child("msctls_progress32", "", 0, 0, 0)
	testEdit = child("EDIT", "", 0x4|0x40|0x800|wsVScroll|wsTabStop, wsExClientEdg, 0) // MULTILINE | AUTOVSCROLL | READONLY
	refreshEnabled()
	dpi, _, _ := getDpiForWindow.Call(settingsWnd)
	settingsDPI = int(dpi)
	layoutSettings()
}

// layoutSettings positions every control for settingsDPI and sizes the window
// to fit. All numbers are 96-DPI pixels.
func layoutSettings() {
	old := settingsFonts
	settingsFonts.text = font(14, 400, "Microsoft YaHei UI", settingsDPI)
	settingsFonts.bold = font(14, 600, "Microsoft YaHei UI", settingsDPI)
	place := func(hwnd uintptr, x, y, w, h int, face uintptr) {
		scale := func(v int) uintptr { return uintptr(v * settingsDPI / 96) }
		moveWindow.Call(hwnd, scale(x), scale(y), scale(w), scale(h), 1)
		send(hwnd, wmSetFont, face, 1)
	}
	y := 14
	for i, r := range rows {
		if r.heading != 0 {
			if i > 0 {
				y += 8
			}
			place(r.heading, 20, y, 350, 22, settingsFonts.bold)
			y += 28
		}
		place(r.label, 20, y+4, 96, 22, settingsFonts.text)
		height := 400 // Of the open list.
		if form[i].hotkey {
			height = 28
		}
		place(r.combo, 120, y, 250, height, settingsFonts.text)
		y += 34
	}
	place(footnote, 20, y+4, 250, 20, settingsFonts.text)
	place(aboutButton, 274, y, 96, 28, settingsFonts.text)
	bottom := y + 36
	place(divider, 387, 14, 2, bottom-28, settingsFonts.text)
	place(testHeading, 404, 14, 336, 22, settingsFonts.bold)
	place(testButton, 404, 42, 104, 30, settingsFonts.text)
	place(testStatus, 518, 40, 222, 38, settingsFonts.text)
	place(testLevel, 404, 84, 336, 6, settingsFonts.text)
	place(testEdit, 404, 98, 336, bottom-98-14, settingsFonts.text)
	deleteObject.Call(old.text)
	deleteObject.Call(old.bold)
	frame := windows.Rect{Right: int32(760 * settingsDPI / 96), Bottom: int32(bottom * settingsDPI / 96)}
	adjustWindowRect.Call(uintptr(unsafe.Pointer(&frame)), settingsStyle, 0, wsExCtrlPar, uintptr(settingsDPI))
	setWindowPos.Call(settingsWnd, 0, 0, 0, uintptr(frame.Right-frame.Left), uintptr(frame.Bottom-frame.Top), 0x2|0x4|0x10) // NOMOVE | NOZORDER | NOACTIVATE
}

func showSettings() {
	if !visible(settingsWnd) {
		// Centre on the monitor the user is working on.
		fg, _, _ := getForeground.Call()
		monitor, _, _ := monitorFromWindow.Call(fg, 2) // MONITOR_DEFAULTTONEAREST
		work := workArea(monitor)
		var r windows.Rect
		getWindowRect.Call(settingsWnd, uintptr(unsafe.Pointer(&r)))
		setWindowPos.Call(settingsWnd, 0, uintptr((work.Left+work.Right-(r.Right-r.Left))/2), uintptr((work.Top+work.Bottom-(r.Bottom-r.Top))/2), 0, 0, 0x1|0x4) // NOSIZE | NOZORDER
	}
	showWindow.Call(settingsWnd, 1) // SW_SHOWNORMAL, also restores a minimized window.
	setForeground.Call(settingsWnd)
}

// fill lists choices in field i's combo and selects the stored value.
func fill(i int, choices []config.Choice) {
	r := &rows[i]
	var selected int
	r.shown, selected = form[i].options(choices, *form[i].value(&cfg))
	send(r.combo, 0x14b, 0, 0) // CB_RESETCONTENT
	for _, c := range r.shown {
		sendMessage.Call(r.combo, 0x143, 0, uintptr(unsafe.Pointer(str(c.Name)))) // CB_ADDSTRING
	}
	send(r.combo, 0x14e, uintptr(selected), 0) // CB_SETCURSEL
}

// reloadChoices refetches the lists that can change while the app runs.
func reloadChoices() {
	for i, f := range form {
		if f.load == nil {
			continue
		}
		go func() {
			choices, err := f.load()
			shared.Lock()
			shared.choices[i] = choices
			if err != nil && !shared.running {
				shared.status = "读取" + f.label + "列表失败：" + err.Error()
			}
			shared.Unlock()
			postMessage.Call(mainWnd, wmChoices, uintptr(i), 0)
			if err != nil {
				postMessage.Call(mainWnd, wmTestRefresh, 0, 0)
			}
		}()
	}
}

// recording is the index of the hotkey field waiting for a key combination.
var recording = -1

// recordHotkey makes field i's button wait for the next key combination;
// clicking it again gives up.
func recordHotkey(i int) {
	if recording >= 0 {
		cancelRecording()
		return
	}
	recording = i
	setCaption(rows[i].combo, "请按下新的快捷键（Esc 取消）")
	desktop.CaptureHotkey(func(value string) {
		shared.Lock()
		shared.hotkey = value
		shared.Unlock()
		postMessage.Call(mainWnd, wmHotkey, 0, 0)
	})
}

func cancelRecording() {
	if recording >= 0 {
		desktop.CaptureHotkey(nil)
		shared.Lock()
		shared.hotkey = ""
		shared.Unlock()
		hotkeyRecorded()
	}
}

// hotkeyRecorded stores what recordHotkey captured; "" keeps the old hotkey.
func hotkeyRecorded() {
	if recording < 0 {
		return
	}
	i := recording
	recording = -1
	shared.Lock()
	value := shared.hotkey
	shared.Unlock()
	if value != "" {
		*form[i].value(&cfg) = value
		changed()
	}
	setCaption(rows[i].combo, desktop.HotkeyName(*form[i].value(&cfg)))
}

// changed publishes cfg after an edit.
func changed() {
	refreshEnabled()
	opts.OnChange(cfg)
	// Both show the hotkey.
	tray(1) // NIM_MODIFY
	invalidateRect.Call(barWnd, 0, 0)
}

func choicesLoaded(i int) {
	shared.Lock()
	choices := shared.choices[i]
	shared.Unlock()
	// Replacing the items would close a list the user has just opened.
	if send(rows[i].combo, 0x157, 0, 0) == 0 { // CB_GETDROPPEDSTATE
		fill(i, choices)
	}
}

func refreshEnabled() {
	for i, f := range form {
		if f.enabled != nil {
			enabled := uintptr(0)
			if f.enabled(&cfg) {
				enabled = 1
			}
			enableWindow.Call(rows[i].combo, enabled)
		}
	}
}

func startTest() {
	ctx, cancel := context.WithCancel(context.Background())
	testCancel = cancel
	shared.Lock()
	shared.text, shared.level, shared.status, shared.running = "", 0, "正在连接…", true
	shared.Unlock()
	refreshTest()
	post := func(change func()) {
		shared.Lock()
		change()
		shared.Unlock()
		postMessage.Call(mainWnd, wmTestRefresh, 0, 0)
	}
	settings := cfg
	go func() {
		err := opts.Test(ctx, settings, func(text string) {
			post(func() { shared.text = text })
		}, func(level float64) {
			post(func() { shared.level, shared.status = level, "正在聆听，请说话…" })
		})
		post(func() {
			shared.running, shared.level, shared.status = false, 0, "测试结束。"+testHint
			if err != nil {
				shared.status = "测试失败：" + err.Error()
			}
		})
	}()
}

func stopTest() {
	if testCancel != nil {
		testCancel()
		testCancel = nil
	}
}

func refreshTest() {
	shared.Lock()
	text, level, status, running := shared.text, shared.level, shared.status, shared.running
	shared.Unlock()
	label := "停止测试"
	if !running {
		label = "开始测试"
		stopTest() // The test ended by itself; release its context.
	}
	setCaption(testButton, label)
	setCaption(testStatus, status)
	send(testLevel, 0x402, uintptr(level*100), 0) // PBM_SETPOS
	if captions[testEdit] != text {
		setCaption(testEdit, text)
		send(testEdit, 0xb1, ^uintptr(0), ^uintptr(0)) // EM_SETSEL to the end
		send(testEdit, 0xb7, 0, 0)                     // EM_SCROLLCARET
	}
}

func settingsProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCommand:
		id, code := int(wParam&0xffff), wParam>>16
		switch {
		case id == idAbout:
			openAbout()
		case id == idTest:
			if testCancel != nil {
				stopTest()
			} else {
				startTest()
			}
		case id >= idField && id < idField+len(form) && form[id-idField].hotkey:
			if code == 0 { // BN_CLICKED
				recordHotkey(id - idField)
			}
		case id >= idField && id < idField+len(form) && code == 1: // CBN_SELCHANGE
			i := id - idField
			*form[i].value(&cfg) = rows[i].shown[int(send(rows[i].combo, 0x147, 0, 0))].Value // CB_GETCURSEL
			changed()
		}
		return 0
	case wmActivate:
		if wParam&0xffff != 0 {
			reloadChoices()
		} else {
			cancelRecording()
		}
	case wmDPIChanged:
		settingsDPI = int(hiword(wParam))
		layoutSettings()
		return 0
	case wmCtlColorStat:
		// Labels and the read-only edit box sit on the white window background.
		setBkMode.Call(wParam, 1) // TRANSPARENT
		if lParam == footnote {
			setTextColor.Call(wParam, rgb(0x70, 0x70, 0x70))
		}
		brush, _, _ := getSysColorBrush.Call(5) // COLOR_WINDOW
		return brush
	case wmClose:
		stopTest()
		showWindow.Call(hwnd, swHide)
		return 0
	}
	return defProc(hwnd, message, wParam, lParam)
}

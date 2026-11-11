package ui

import (
	"errors"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"xvoice/internal/config"
	"xvoice/internal/desktop"
	"xvoice/internal/session"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")

	registerClass      = user32.NewProc("RegisterClassExW")
	createWindow       = user32.NewProc("CreateWindowExW")
	defWindowProc      = user32.NewProc("DefWindowProcW")
	destroyWindow      = user32.NewProc("DestroyWindow")
	showWindow         = user32.NewProc("ShowWindow")
	isWindowVisible    = user32.NewProc("IsWindowVisible")
	setWindowPos       = user32.NewProc("SetWindowPos")
	moveWindow         = user32.NewProc("MoveWindow")
	getWindowRect      = user32.NewProc("GetWindowRect")
	findWindow         = user32.NewProc("FindWindowW")
	getWindowProcess   = user32.NewProc("GetWindowThreadProcessId")
	allowSetForeground = user32.NewProc("AllowSetForegroundWindow")
	getMessage         = user32.NewProc("GetMessageW")
	translateMessage   = user32.NewProc("TranslateMessage")
	dispatchMessage    = user32.NewProc("DispatchMessageW")
	isDialogMessage    = user32.NewProc("IsDialogMessageW")
	postMessage        = user32.NewProc("PostMessageW")
	sendMessage        = user32.NewProc("SendMessageW")
	postQuitMessage    = user32.NewProc("PostQuitMessage")
	registerMessage    = user32.NewProc("RegisterWindowMessageW")
	messageBox         = user32.NewProc("MessageBoxW")
	invalidateRect     = user32.NewProc("InvalidateRect")
	beginPaint         = user32.NewProc("BeginPaint")
	endPaint           = user32.NewProc("EndPaint")
	getClientRect      = user32.NewProc("GetClientRect")
	clientToScreen     = user32.NewProc("ClientToScreen")
	fillRect           = user32.NewProc("FillRect")
	drawText           = user32.NewProc("DrawTextW")
	loadCursor         = user32.NewProc("LoadCursorW")
	loadIcon           = user32.NewProc("LoadIconW")
	loadImage          = user32.NewProc("LoadImageW")
	getSystemMetrics   = user32.NewProc("GetSystemMetrics")
	setTimer           = user32.NewProc("SetTimer")
	killTimer          = user32.NewProc("KillTimer")
	setWindowText      = user32.NewProc("SetWindowTextW")
	enableWindow       = user32.NewProc("EnableWindow")
	setForeground      = user32.NewProc("SetForegroundWindow")
	getForeground      = user32.NewProc("GetForegroundWindow")
	getCursorPos       = user32.NewProc("GetCursorPos")
	createPopupMenu    = user32.NewProc("CreatePopupMenu")
	appendMenu         = user32.NewProc("AppendMenuW")
	trackPopupMenu     = user32.NewProc("TrackPopupMenu")
	destroyMenu        = user32.NewProc("DestroyMenu")
	trackMouseEvent    = user32.NewProc("TrackMouseEvent")
	monitorFromPoint   = user32.NewProc("MonitorFromPoint")
	monitorFromWindow  = user32.NewProc("MonitorFromWindow")
	getMonitorInfo     = user32.NewProc("GetMonitorInfoW")
	getSysColorBrush   = user32.NewProc("GetSysColorBrush")
	getDpiForWindow    = user32.NewProc("GetDpiForWindow")
	adjustWindowRect   = user32.NewProc("AdjustWindowRectExForDpi")
	getDpiForMonitor   = shcore.NewProc("GetDpiForMonitor")
	initCommonControls = comctl32.NewProc("InitCommonControlsEx")
	createFont         = gdi32.NewProc("CreateFontW")
	selectObject       = gdi32.NewProc("SelectObject")
	deleteObject       = gdi32.NewProc("DeleteObject")
	setBkMode          = gdi32.NewProc("SetBkMode")
	setTextColor       = gdi32.NewProc("SetTextColor")
	createSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	getStockObject     = gdi32.NewProc("GetStockObject")
	ellipse            = gdi32.NewProc("Ellipse")
	roundRect          = gdi32.NewProc("RoundRect")
	createCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	createBitmap       = gdi32.NewProc("CreateCompatibleBitmap")
	bitBlt             = gdi32.NewProc("BitBlt")
	deleteDC           = gdi32.NewProc("DeleteDC")
	notifyIcon         = shell32.NewProc("Shell_NotifyIconW")
	getModuleHandle    = kernel32.NewProc("GetModuleHandleW")
	createMutex        = kernel32.NewProc("CreateMutexW")
	dwmSetAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	wmDestroy       = 0x0002
	wmActivate      = 0x0006
	wmPaint         = 0x000f
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmMouseActivate = 0x0021
	wmSetFont       = 0x0030
	wmNCHitTest     = 0x0084
	wmCommand       = 0x0111
	wmTimer         = 0x0113
	wmCtlColorStat  = 0x0138
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmMouseLeave    = 0x02a3
	wmDPIChanged    = 0x02e0
	wmApp           = 0x8000
	wmBarRefresh    = wmApp + 1 // A new session.State is waiting in latest.
	wmTestRefresh   = wmApp + 2 // The settings test progressed.
	wmChoices       = wmApp + 3 // wParam: index of the field whose choices loaded.
	wmShowSettings  = wmApp + 4 // Also posted by a second instance.
	wmHotkey        = wmApp + 5 // A recorded hotkey is waiting in shared.
	wmTray          = wmApp + 10

	wsPopup       = 0x80000000
	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsCaption     = 0x00c00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsVScroll     = 0x00200000
	wsTabStop     = 0x00010000
	wsExTopmost   = 0x00000008
	wsExToolWin   = 0x00000080
	wsExNoActive  = 0x08000000
	wsExClientEdg = 0x00000200
	wsExCtrlPar   = 0x00010000

	swHide         = 0
	swShow         = 5
	swShowNoActive = 4
)

type point struct{ X, Y int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

type paintStruct struct {
	HDC       uintptr
	Erase     int32
	Paint     windows.Rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type wndClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

var (
	opts      Options
	cfg       config.Config // The UI thread's copy of the settings.
	instance  uintptr
	mainWnd   uintptr // Hidden; owns the tray icon and receives posted work.
	iconSmall uintptr
	iconBig   uintptr
	stateMu   sync.Mutex
	latest    session.State
	taskbarUp uintptr // "TaskbarCreated": Explorer restarted, re-add the tray icon.
)

func str(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func rgb(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

func loword(v uintptr) int32 { return int32(int16(v)) }
func hiword(v uintptr) int32 { return int32(int16(v >> 16)) }

func register(name string, proc func(hwnd, message, wParam, lParam uintptr) uintptr, background uintptr) {
	cursor, _, _ := loadCursor.Call(0, 32512) // IDC_ARROW
	class := wndClass{WndProc: syscall.NewCallback(proc), Instance: instance, Cursor: cursor, Icon: iconBig, IconSm: iconSmall, Background: background, ClassName: str(name)}
	class.Size = uint32(unsafe.Sizeof(class))
	registerClass.Call(uintptr(unsafe.Pointer(&class)))
}

func defProc(hwnd, message, wParam, lParam uintptr) uintptr {
	r, _, _ := defWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

func send(hwnd, message, wParam, lParam uintptr) uintptr {
	r, _, _ := sendMessage.Call(hwnd, message, wParam, lParam)
	return r
}

func visible(hwnd uintptr) bool {
	r, _, _ := isWindowVisible.Call(hwnd)
	return r != 0
}

// workArea is the usable rectangle of a monitor.
func workArea(monitor uintptr) windows.Rect {
	info := struct {
		Size          uint32
		Monitor, Work windows.Rect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	return info.Work
}

// font creates a face sized in 96-DPI pixels for a window at dpi.
func font(size, weight int, face string, dpi int) uintptr {
	f, _, _ := createFont.Call(uintptr(-int32(size*dpi/96)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(str(face)))) // DEFAULT_CHARSET, CLEARTYPE_QUALITY
	return f
}

// appIcon loads the executable's icon at a system metric size (SM_CXICON or
// SM_CXSMICON), falling back to the stock one for builds without resources.
func appIcon(metric uintptr) uintptr {
	size, _, _ := getSystemMetrics.Call(metric)
	icon, _, _ := loadImage.Call(instance, 1, 1, size, size, 0) // Resource 1, IMAGE_ICON
	if icon == 0 {
		icon, _, _ = loadIcon.Call(0, 32512) // IDI_APPLICATION
	}
	return icon
}

// SingleInstance reports whether this is the only running copy. If not, the
// running copy is asked to show its settings window instead.
func SingleInstance() bool {
	if _, _, err := createMutex.Call(0, 0, uintptr(unsafe.Pointer(str("Local\\XVoice.SingleInstance")))); err != windows.ERROR_ALREADY_EXISTS {
		return true
	}
	if running, _, _ := findWindow.Call(uintptr(unsafe.Pointer(str("XVoiceMain"))), 0); running != 0 {
		var process uint32
		getWindowProcess.Call(running, uintptr(unsafe.Pointer(&process)))
		// Only the process the user just launched may hand over the foreground.
		allowSetForeground.Call(uintptr(process))
		postMessage.Call(running, wmShowSettings, 0, 0)
	}
	return false
}

// Alert shows a blocking error box, for failures before the UI is up.
func Alert(message string) {
	messageBox.Call(0, uintptr(unsafe.Pointer(str(message))), uintptr(unsafe.Pointer(str("XVoice"))), 0x10) // MB_ICONERROR
}

// Update publishes a session state from any goroutine.
func Update(s session.State) {
	stateMu.Lock()
	latest = s
	stateMu.Unlock()
	postMessage.Call(mainWnd, wmBarRefresh, 0, 0)
}

// Run creates the tray icon, bar and settings window and pumps messages until
// the user chooses Exit. Visual styles and per-monitor DPI awareness come
// from the manifest embedded next to the icon (see cmd/xvoice).
func Run(o Options) error {
	runtime.LockOSThread()
	opts, cfg = o, o.Config
	instance, _, _ = getModuleHandle.Call(0)
	classes := struct{ Size, Classes uint32 }{8, 0x20} // ICC_PROGRESS_CLASS
	initCommonControls.Call(uintptr(unsafe.Pointer(&classes)))
	iconSmall, iconBig = appIcon(49), appIcon(11) // SM_CXSMICON, SM_CXICON
	taskbarUp, _, _ = registerMessage.Call(uintptr(unsafe.Pointer(str("TaskbarCreated"))))
	register("XVoiceMain", mainProc, 0)
	register("XVoiceBar", barProc, 0)
	windowColor, _, _ := getSysColorBrush.Call(5) // COLOR_WINDOW
	register("XVoiceSettings", settingsProc, windowColor)
	mainWnd, _, _ = createWindow.Call(0, uintptr(unsafe.Pointer(str("XVoiceMain"))), uintptr(unsafe.Pointer(str("XVoice"))), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if mainWnd == 0 {
		return errors.New("创建主窗口失败")
	}
	createBar()
	createSettings()
	tray(0) // NIM_ADD
	if o.ShowSettings {
		showSettings()
	} else {
		showBarIdle()
	}
	var m msg
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return nil
		}
		if visible(settingsWnd) {
			// Tab and arrow-key navigation between the settings controls.
			if r, _, _ := isDialogMessage.Call(settingsWnd, uintptr(unsafe.Pointer(&m))); r != 0 {
				continue
			}
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

type notifyData struct {
	Size        uint32
	Wnd         uintptr
	ID          uint32
	Flags       uint32
	CallbackMsg uint32
	Icon        uintptr
	Tip         [128]uint16
	State       uint32
	StateMask   uint32
	Info        [256]uint16
	Version     uint32
	InfoTitle   [64]uint16
	InfoFlags   uint32
	GUID        windows.GUID
	BalloonIcon uintptr
}

// tray adds (0), updates (1) or removes (2) the notification-area icon.
func tray(action uintptr) {
	data := notifyData{Wnd: mainWnd, ID: 1, Flags: 1 | 2 | 4, CallbackMsg: wmTray, Icon: iconSmall} // MESSAGE | ICON | TIP
	data.Size = uint32(unsafe.Sizeof(data))
	copy(data.Tip[:], windows.StringToUTF16("XVoice · "+desktop.HotkeyName(cfg.Hotkey)))
	notifyIcon.Call(action, uintptr(unsafe.Pointer(&data)))
}

const (
	menuBar = iota + 1
	menuSettings
	menuExit
)

func trayMenu() {
	menu, _, _ := createPopupMenu.Call()
	defer destroyMenu.Call(menu)
	appendMenu.Call(menu, 0, menuBar, uintptr(unsafe.Pointer(str("显示语音条"))))
	appendMenu.Call(menu, 0, menuSettings, uintptr(unsafe.Pointer(str("设置…"))))
	appendMenu.Call(menu, 0x800, 0, 0) // MF_SEPARATOR
	appendMenu.Call(menu, 0, menuExit, uintptr(unsafe.Pointer(str("退出"))))
	var p point
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	// Required so the menu closes when the user clicks elsewhere.
	setForeground.Call(mainWnd)
	choice, _, _ := trackPopupMenu.Call(menu, 0x100|0x2, uintptr(p.X), uintptr(p.Y), 0, mainWnd, 0) // RETURNCMD | RIGHTBUTTON
	postMessage.Call(mainWnd, 0, 0, 0)
	switch choice {
	case menuBar:
		showBarIdle()
	case menuSettings:
		showSettings()
	case menuExit:
		destroyWindow.Call(mainWnd)
	}
}

func mainProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmBarRefresh:
		stateMu.Lock()
		s := latest
		stateMu.Unlock()
		applyState(s)
		return 0
	case wmTestRefresh:
		refreshTest()
		return 0
	case wmChoices:
		choicesLoaded(int(wParam))
		return 0
	case wmShowSettings:
		showSettings()
		return 0
	case wmHotkey:
		hotkeyRecorded()
		return 0
	case wmTray:
		switch lParam & 0xffff {
		case wmLButtonUp:
			if visible(barWnd) {
				showWindow.Call(barWnd, swHide)
			} else {
				showBarIdle()
			}
		case wmRButtonUp:
			trayMenu()
		}
		return 0
	case wmDestroy:
		tray(2) // NIM_DELETE
		stopTest()
		opts.Stop()
		postQuitMessage.Call(0)
		return 0
	}
	if message == taskbarUp && taskbarUp != 0 {
		tray(0)
		return 0
	}
	return defProc(hwnd, message, wParam, lParam)
}

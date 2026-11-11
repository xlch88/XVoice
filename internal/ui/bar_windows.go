package ui

import (
	"image"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/xlch88/XVoice/internal/desktop"
	"github.com/xlch88/XVoice/internal/session"
)

// The floating bar never takes focus (WS_EX_NOACTIVATE + MA_NOACTIVATE), so
// clicking it leaves the caret in the destination app, like Win+H.

const (
	barW, barH = 460, 84 // At 96 DPI, like every layout number below.
	hideTimer  = 1
	hideDelay  = 5000
)

const (
	hitNone = iota
	hitMic
	hitSettings
	hitClose
)

var (
	barWnd     uintptr
	barDPI     int // Scale of the monitor the bar is on.
	barState   session.State
	barText    string // Last transcript; survives the dictation ending.
	barHover   int
	barPressed int
	barClosed  bool // The user closed the bar mid-dictation; stay hidden until it has ended.
	barFonts   struct{ status, text, icon uintptr }
)

func px(v int) int32 { return int32(v * barDPI / 96) }

func createBar() {
	barWnd, _, _ = createWindow.Call(wsExTopmost|wsExToolWin|wsExNoActive, uintptr(unsafe.Pointer(str("XVoiceBar"))), uintptr(unsafe.Pointer(str("XVoice"))), wsPopup,
		0, 0, 0, 0, 0, 0, instance, 0)
	round := int32(2) // DWMWCP_ROUND: Windows 11 rounded corners and shadow.
	dwmSetAttribute.Call(barWnd, 33, uintptr(unsafe.Pointer(&round)), 4)
	setBarDPI(96)
}

// setBarDPI sizes the bar and its fonts for the monitor it is on.
func setBarDPI(dpi int) {
	if dpi == barDPI {
		return
	}
	barDPI = dpi
	for _, f := range []uintptr{barFonts.status, barFonts.text, barFonts.icon} {
		deleteObject.Call(f)
	}
	barFonts.status = font(12, 400, "Microsoft YaHei UI", dpi)
	barFonts.text = font(16, 400, "Microsoft YaHei UI", dpi)
	barFonts.icon = font(16, 400, "Segoe MDL2 Assets", dpi)
	setWindowPos.Call(barWnd, 0, 0, 0, uintptr(px(barW)), uintptr(px(barH)), 0x2|0x4|0x10) // NOMOVE | NOZORDER | NOACTIVATE
}

// Button rectangles in 96-DPI client coordinates.
var (
	micCenter   = image.Pt(42, 42)
	settingsBox = image.Rect(384, 28, 412, 56)
	closeBox    = image.Rect(418, 28, 446, 56)
)

func scaled(r image.Rectangle) windows.Rect {
	return windows.Rect{Left: px(r.Min.X), Top: px(r.Min.Y), Right: px(r.Max.X), Bottom: px(r.Max.Y)}
}

func barHit(x, y int32) int {
	p := image.Pt(int(x)*96/barDPI, int(y)*96/barDPI)
	d := p.Sub(micCenter)
	switch {
	case d.X*d.X+d.Y*d.Y <= 24*24:
		return hitMic
	case p.In(settingsBox):
		return hitSettings
	case p.In(closeBox):
		return hitClose
	}
	return hitNone
}

// placeBar puts the bar just below the caret (or above it near the bottom of
// the screen), falling back to the bottom centre of the active monitor.
func placeBar(s session.State) {
	var monitor uintptr
	if s.Placed {
		monitor, _, _ = monitorFromPoint.Call(uintptr(uint32(s.Anchor.Min.X))|uintptr(uint32(s.Anchor.Min.Y))<<32, 2)
	} else {
		fg, _, _ := getForeground.Call()
		monitor, _, _ = monitorFromWindow.Call(fg, 2) // MONITOR_DEFAULTTONEAREST
	}
	var dpi, dpiY uint32
	getDpiForMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&dpi)), uintptr(unsafe.Pointer(&dpiY))) // MDT_EFFECTIVE_DPI
	setBarDPI(int(max(dpi, 96)))
	w, h, gap := px(barW), px(barH), px(12)
	work := workArea(monitor)
	x, y := (work.Left+work.Right-w)/2, work.Bottom-h-px(48)
	if s.Placed {
		x = int32(s.Anchor.Min.X) - px(24)
		switch {
		case int32(s.Anchor.Max.Y)+gap+h <= work.Bottom:
			y = int32(s.Anchor.Max.Y) + gap
		case int32(s.Anchor.Min.Y)-gap-h >= work.Top:
			y = int32(s.Anchor.Min.Y) - gap - h
		default:
			x = (work.Left + work.Right - w) / 2
		}
	}
	x = max(work.Left, min(x, work.Right-w))
	y = max(work.Top, min(y, work.Bottom-h))
	setWindowPos.Call(barWnd, ^uintptr(0), uintptr(x), uintptr(y), 0, 0, 0x1|0x10) // HWND_TOPMOST, NOSIZE | NOACTIVATE
}

func showBarIdle() {
	if !visible(barWnd) {
		placeBar(session.State{})
	}
	showWindow.Call(barWnd, swShowNoActive)
	invalidateRect.Call(barWnd, 0, 0)
}

func applyState(s session.State) {
	was := barState.Phase
	if was == session.Idle && s.Phase != session.Idle {
		if cfg.Sound != "off" {
			play(startSound)
		}
		barText = ""
		placeBar(s)
	}
	if was != session.Idle && s.Phase == session.Idle && cfg.Sound != "off" {
		play(stopSound)
	}
	if s.Text != "" {
		barText = session.Format(s.Text, cfg.Punctuation)
	}
	barState = s
	if barClosed {
		// The dictation being aborted still reports its last states.
		barClosed = s.Phase != session.Idle
		return
	}
	if s.Phase == session.Idle {
		// Also surfaces "focus is not a text field" when nothing started.
		if !visible(barWnd) {
			placeBar(s)
		}
		setTimer.Call(barWnd, hideTimer, hideDelay, 0)
	} else {
		killTimer.Call(barWnd, hideTimer)
	}
	showWindow.Call(barWnd, swShowNoActive)
	invalidateRect.Call(barWnd, 0, 0)
}

// tail keeps the end of text that fits in rect, prefixed with an ellipsis.
func tail(hdc uintptr, text string, rect windows.Rect) string {
	runes := []rune(text)
	runes = runes[max(0, len(runes)-160):]
	for i := range runes {
		candidate := string(runes[i:])
		if i > 0 || len(runes) < utf8.RuneCountInString(text) {
			candidate = "…" + candidate
		}
		r := rect
		drawText.Call(hdc, uintptr(unsafe.Pointer(str(candidate))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), 0x10|0x400|0x800) // WORDBREAK | CALCRECT | NOPREFIX
		if r.Bottom <= rect.Bottom {
			return candidate
		}
	}
	return ""
}

func paintBar(hdc uintptr) {
	var client windows.Rect
	getClientRect.Call(barWnd, uintptr(unsafe.Pointer(&client)))
	background, _, _ := createSolidBrush.Call(rgb(0x20, 0x20, 0x20))
	fillRect.Call(hdc, uintptr(unsafe.Pointer(&client)), background)
	deleteObject.Call(background)
	setBkMode.Call(hdc, 1) // TRANSPARENT
	nullPen, _, _ := getStockObject.Call(8)
	selectObject.Call(hdc, nullPen)

	// Microphone button with a level halo while listening.
	circle := func(r int, color uintptr) {
		brush, _, _ := createSolidBrush.Call(color)
		old, _, _ := selectObject.Call(hdc, brush)
		ellipse.Call(hdc, uintptr(px(micCenter.X-r)), uintptr(px(micCenter.Y-r)), uintptr(px(micCenter.X+r)), uintptr(px(micCenter.Y+r)))
		selectObject.Call(hdc, old)
		deleteObject.Call(brush)
	}
	hotkey := desktop.HotkeyName(cfg.Hotkey)
	micColor, status := rgb(0x3a, 0x3a, 0x3a), barState.Message
	switch barState.Phase {
	case session.Connecting:
		micColor, status = rgb(0x55, 0x55, 0x55), "正在连接…"
	case session.Listening:
		circle(22+int(barState.Level*8), rgb(0x1f, 0x3f, 0x7a))
		micColor, status = rgb(0x2f, 0x7f, 0xff), "正在聆听 · 按任意键或 "+hotkey+" 停止"
	case session.Finishing:
		micColor, status = rgb(0x55, 0x55, 0x55), "正在完成…"
	default:
		if status == "" {
			status = "在其他应用的输入框中按 " + hotkey + " 开始"
		}
	}
	if barHover == hitMic {
		micColor += rgb(0x10, 0x10, 0x10)
	}
	circle(20, micColor)
	selectObject.Call(hdc, barFonts.icon)
	setTextColor.Call(hdc, rgb(0xff, 0xff, 0xff))
	micRect := scaled(image.Rect(micCenter.X-20, micCenter.Y-20, micCenter.X+20, micCenter.Y+20))
	drawText.Call(hdc, uintptr(unsafe.Pointer(str(""))), 1, uintptr(unsafe.Pointer(&micRect)), 0x1|0x4|0x20) // CENTER | VCENTER | SINGLELINE

	for _, b := range []struct {
		hit   int
		box   image.Rectangle
		glyph string
	}{{hitSettings, settingsBox, ""}, {hitClose, closeBox, ""}} {
		r := scaled(b.box)
		if barHover == b.hit {
			brush, _, _ := createSolidBrush.Call(rgb(0x3a, 0x3a, 0x3a))
			old, _, _ := selectObject.Call(hdc, brush)
			roundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom), uintptr(px(6)), uintptr(px(6)))
			selectObject.Call(hdc, old)
			deleteObject.Call(brush)
		}
		setTextColor.Call(hdc, rgb(0xc8, 0xc8, 0xc8))
		drawText.Call(hdc, uintptr(unsafe.Pointer(str(b.glyph))), 1, uintptr(unsafe.Pointer(&r)), 0x1|0x4|0x20)
	}

	selectObject.Call(hdc, barFonts.status)
	setTextColor.Call(hdc, rgb(0x9a, 0x9a, 0x9a))
	statusRect := scaled(image.Rect(78, 12, 374, 30))
	drawText.Call(hdc, uintptr(unsafe.Pointer(str(status))), ^uintptr(0), uintptr(unsafe.Pointer(&statusRect)), 0x20|0x8000|0x800) // SINGLELINE | END_ELLIPSIS | NOPREFIX

	text, color := barText, rgb(0xf0, 0xf0, 0xf0)
	if text == "" && barState.Phase != session.Idle {
		text, color = "请开始说话", rgb(0x70, 0x70, 0x70)
	}
	selectObject.Call(hdc, barFonts.text)
	setTextColor.Call(hdc, color)
	textRect := scaled(image.Rect(78, 32, 374, 76))
	text = tail(hdc, text, textRect)
	drawText.Call(hdc, uintptr(unsafe.Pointer(str(text))), ^uintptr(0), uintptr(unsafe.Pointer(&textRect)), 0x10|0x800)
}

func barProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmMouseActivate:
		return 3 // MA_NOACTIVATE
	case wmNCHitTest:
		var origin point
		clientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin)))
		if barHit(loword(lParam)-origin.X, hiword(lParam)-origin.Y) == hitNone {
			return 2 // HTCAPTION: drag the bar from anywhere else.
		}
		return 1 // HTCLIENT
	case wmDPIChanged:
		// Dragged onto a monitor with another scale.
		setBarDPI(int(hiword(wParam)))
		invalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmMouseMove:
		if hit := barHit(loword(lParam), hiword(lParam)); hit != barHover {
			barHover = hit
			invalidateRect.Call(hwnd, 0, 0)
			track := struct {
				Size, Flags uint32
				Wnd         uintptr
				Hover       uint32
			}{Flags: 2, Wnd: hwnd} // TME_LEAVE
			track.Size = uint32(unsafe.Sizeof(track))
			trackMouseEvent.Call(uintptr(unsafe.Pointer(&track)))
		}
		return 0
	case wmMouseLeave:
		barHover = hitNone
		invalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmLButtonDown:
		barPressed = barHit(loword(lParam), hiword(lParam))
		return 0
	case wmLButtonUp:
		hit := barHit(loword(lParam), hiword(lParam))
		if hit != barPressed {
			return 0
		}
		barPressed = hitNone
		switch hit {
		case hitMic:
			go opts.Toggle()
		case hitSettings, hitClose:
			// Both end the dictation and put the bar away; opening settings
			// would take the focus from the text field anyway.
			barClosed = barState.Phase != session.Idle
			go opts.Stop()
			showWindow.Call(hwnd, swHide)
			if hit == hitSettings {
				showSettings()
			}
		}
		return 0
	case wmTimer:
		if wParam == hideTimer {
			killTimer.Call(hwnd, hideTimer)
			if barState.Phase == session.Idle && barHover == hitNone {
				showWindow.Call(hwnd, swHide)
			} else if barState.Phase == session.Idle {
				setTimer.Call(hwnd, hideTimer, hideDelay, 0)
			}
		}
		return 0
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		var r windows.Rect
		getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		// Double-buffered so level updates do not flicker.
		memory, _, _ := createCompatibleDC.Call(hdc)
		bitmap, _, _ := createBitmap.Call(hdc, uintptr(r.Right), uintptr(r.Bottom))
		old, _, _ := selectObject.Call(memory, bitmap)
		paintBar(memory)
		bitBlt.Call(hdc, 0, 0, uintptr(r.Right), uintptr(r.Bottom), memory, 0, 0, 0x00cc0020) // SRCCOPY
		selectObject.Call(memory, old)
		deleteObject.Call(bitmap)
		deleteDC.Call(memory)
		endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	}
	return defProc(hwnd, message, wParam, lParam)
}

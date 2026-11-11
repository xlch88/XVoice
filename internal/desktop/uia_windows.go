package desktop

import (
	"image"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/xlch88/XVoice/internal/winapi"
)

var (
	clsidAutomation = windows.GUID{Data1: 0xff48dba4, Data2: 0x60ef, Data3: 0x4201, Data4: [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidAutomation   = windows.GUID{Data1: 0x30cbe57d, Data2: 0xd9d0, Data3: 0x452a, Data4: [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}
)

// vtable slots
const (
	autoFocused       = 8  // IUIAutomation::GetFocusedElement
	autoControlWalker = 14 // ::get_ControlViewWalker
	elemRuntimeID     = 4  // IUIAutomationElement::GetRuntimeId
	elemPattern       = 16 // ::GetCurrentPattern
	elemProcessID     = 20 // ::get_CurrentProcessId
	elemControlType   = 21 // ::get_CurrentControlType
	elemEnabled       = 28 // ::get_CurrentIsEnabled
	elemPassword      = 35 // ::get_CurrentIsPassword
	elemBounds        = 43 // ::get_CurrentBoundingRectangle
	walkerParent      = 3  // IUIAutomationTreeWalker::GetParentElement
	valueReadOnly     = 5  // IUIAutomationValuePattern::get_CurrentIsReadOnly
	textSelection     = 5  // IUIAutomationTextPattern::GetSelection
	textDocument      = 7  // ::get_DocumentRange
	rangeAttribute    = 9  // IUIAutomationTextRange::GetAttributeValue
	rangeRects        = 10 // ::GetBoundingRectangles
	arrayLength       = 3  // IUIAutomationTextRangeArray::get_Length
	arrayElement      = 4  // ::GetElement
)

const (
	patternValue    = 10002
	patternText     = 10014
	attrReadOnly    = 40015
	typeEdit        = 50004
	typeDocument    = 50030
	typeCombo       = 50003
	typeCustom      = 50025
	typeGroup       = 50026
	typePane        = 50033
	typeText        = 50020
	maxEditorClimbs = 5
)

var engine *winapi.Object // Created and used on the MTA worker; free-threaded.

// editable reports whether element accepts typed text: an enabled, non-password
// edit/document/combo whose value or text is not read-only.
func editable(element *winapi.Object) bool {
	var enabled, password, kind int32
	if winapi.Failed(element.Call(elemEnabled, uintptr(unsafe.Pointer(&enabled)))) || enabled == 0 ||
		winapi.Failed(element.Call(elemPassword, uintptr(unsafe.Pointer(&password)))) || password != 0 {
		return false
	}
	element.Call(elemControlType, uintptr(unsafe.Pointer(&kind)))
	if kind != typeEdit && kind != typeDocument && kind != typeCombo {
		return false
	}
	var pattern *winapi.Object
	if !winapi.Failed(element.Call(elemPattern, patternValue, uintptr(unsafe.Pointer(&pattern)))) && pattern != nil {
		var readOnly int32
		hr := pattern.Call(valueReadOnly, uintptr(unsafe.Pointer(&readOnly)))
		pattern.Release()
		if !winapi.Failed(hr) && readOnly == 0 {
			return true
		}
	}
	pattern = nil
	if winapi.Failed(element.Call(elemPattern, patternText, uintptr(unsafe.Pointer(&pattern)))) || pattern == nil {
		return false
	}
	var document *winapi.Object
	hr := pattern.Call(textDocument, uintptr(unsafe.Pointer(&document)))
	pattern.Release()
	if winapi.Failed(hr) || document == nil {
		return false
	}
	defer document.Release()
	var value winapi.Variant
	defer value.Clear()
	// VT_BOOL false: the document as a whole is writable.
	return !winapi.Failed(document.Call(rangeAttribute, attrReadOnly, uintptr(unsafe.Pointer(&value)))) && value.VT == 11 && uintptr(value.Ptr)&0xffff == 0
}

// findEditor returns the editable element at or a few wrapper levels above
// focused (browsers and Electron often focus a child of the real editor).
// The result is owned by the caller.
func findEditor(focused *winapi.Object) *winapi.Object {
	if focused == nil {
		return nil
	}
	var walker *winapi.Object
	engine.Call(autoControlWalker, uintptr(unsafe.Pointer(&walker)))
	defer walker.Release()
	focused.AddRef()
	element := focused
	for range maxEditorClimbs {
		if editable(element) {
			return element
		}
		var kind int32
		element.Call(elemControlType, uintptr(unsafe.Pointer(&kind)))
		if walker == nil || (kind != typeText && kind != typeCustom && kind != typeGroup && kind != typePane) {
			break
		}
		var parent *winapi.Object
		walker.Call(walkerParent, uintptr(unsafe.Pointer(element)), uintptr(unsafe.Pointer(&parent)))
		element.Release()
		if element = parent; element == nil {
			return nil
		}
	}
	element.Release()
	return nil
}

func runtimeID(element *winapi.Object) []int32 {
	var array winapi.SafeArray
	if winapi.Failed(element.Call(elemRuntimeID, uintptr(unsafe.Pointer(&array)))) || array == 0 {
		return nil
	}
	defer array.Destroy()
	lower, upper := array.Bounds()
	if upper < lower || upper-lower > 1000 {
		return nil
	}
	id := make([]int32, 0, upper-lower+1)
	for i := lower; i <= upper; i++ {
		var v int32
		if !array.Get(i, unsafe.Pointer(&v)) {
			return nil
		}
		id = append(id, v)
	}
	return id
}

func editorID(focused *winapi.Object) []int32 {
	editor := findEditor(focused)
	if editor == nil {
		return nil
	}
	defer editor.Release()
	return runtimeID(editor)
}

// caretRect asks the text provider where the selection is; empty selections
// often report nothing, in which case the editor bounds are used.
func caretRect(editor *winapi.Object) (image.Rectangle, bool) {
	var pattern *winapi.Object
	if !winapi.Failed(editor.Call(elemPattern, patternText, uintptr(unsafe.Pointer(&pattern)))) && pattern != nil {
		var ranges *winapi.Object
		pattern.Call(textSelection, uintptr(unsafe.Pointer(&ranges)))
		pattern.Release()
		if ranges != nil {
			var count int32
			ranges.Call(arrayLength, uintptr(unsafe.Pointer(&count)))
			var first *winapi.Object
			if count > 0 {
				ranges.Call(arrayElement, 0, uintptr(unsafe.Pointer(&first)))
			}
			ranges.Release()
			if first != nil {
				var rects winapi.SafeArray
				first.Call(rangeRects, uintptr(unsafe.Pointer(&rects)))
				first.Release()
				if rects != 0 {
					defer rects.Destroy()
					// Doubles in groups of left, top, width, height; take the last line.
					lower, upper := rects.Bounds()
					if upper-lower+1 >= 4 {
						var v [4]float64
						for i := range v {
							rects.Get(upper-3+int32(i), unsafe.Pointer(&v[i]))
						}
						x, y := int(math.Round(v[0]+v[2])), int(math.Round(v[1]))
						return image.Rect(x, y, x+1, y+int(math.Round(v[3]))), true
					}
				}
			}
		}
	}
	var r windows.Rect
	if winapi.Failed(editor.Call(elemBounds, uintptr(unsafe.Pointer(&r)))) || r.Right <= r.Left {
		return image.Rectangle{}, false
	}
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)), true
}

// uiaInit creates the automation client. It deliberately subscribes to no
// events: a focus-changed subscription makes every application on the machine
// keep its accessibility tree live for as long as this program runs. While a
// target is locked it is polled instead (see Start).
func uiaInit() error {
	var err error
	engine, err = winapi.Create(&clsidAutomation, &iidAutomation)
	return err
}

func uiaClose() {
	engine.Release()
	engine = nil
}

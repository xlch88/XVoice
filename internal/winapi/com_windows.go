// Package winapi holds the small amount of raw COM plumbing shared by the
// Windows implementations. Everything goes through syscall; no cgo.
package winapi

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32           = windows.NewLazySystemDLL("ole32.dll")
	oleaut32        = windows.NewLazySystemDLL("oleaut32.dll")
	coInitializeEx  = ole32.NewProc("CoInitializeEx")
	coUninitialize  = ole32.NewProc("CoUninitialize")
	coCreate        = ole32.NewProc("CoCreateInstance")
	coTaskMemFree   = ole32.NewProc("CoTaskMemFree")
	propClear       = ole32.NewProc("PropVariantClear")
	variantClear    = oleaut32.NewProc("VariantClear")
	safeArrayDelete = oleaut32.NewProc("SafeArrayDestroy")
	safeArrayLower  = oleaut32.NewProc("SafeArrayGetLBound")
	safeArrayUpper  = oleaut32.NewProc("SafeArrayGetUBound")
	safeArrayGet    = oleaut32.NewProc("SafeArrayGetElement")
)

// Object is any COM interface pointer. Methods are addressed by vtable slot;
// callers name the slots with constants next to the interface they use.
type Object struct{ vtable *[256]uintptr }

func (o *Object) Call(slot int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtable[slot], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *Object) AddRef() { o.Call(1) }

func (o *Object) Release() {
	if o != nil {
		o.Call(2)
	}
}

func Failed(hr uintptr) bool { return int32(hr) < 0 }

func HRError(what string, hr uintptr) error { return fmt.Errorf("%s: 0x%08x", what, uint32(hr)) }

// InitMTA must be paired with Uninit on the same locked OS thread.
func InitMTA() error {
	// S_FALSE (already initialized) is still a successful, counted init.
	if hr, _, _ := coInitializeEx.Call(0, 0); Failed(hr) {
		return HRError("CoInitializeEx", hr)
	}
	return nil
}

func Uninit() { coUninitialize.Call() }

func Create(clsid, iid *windows.GUID) (*Object, error) {
	var o *Object
	if hr, _, _ := coCreate.Call(uintptr(unsafe.Pointer(clsid)), 0, 1|4, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&o))); Failed(hr) {
		return nil, HRError("CoCreateInstance", hr)
	}
	return o, nil
}

// TakeString converts and frees a CoTaskMemAlloc'ed UTF-16 string.
func TakeString(p *uint16) string {
	defer coTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p)
}

// Variant matches VARIANT / PROPVARIANT layout on 64-bit Windows.
type Variant struct {
	VT  uint16
	_   [6]byte
	Ptr unsafe.Pointer
	_   uintptr
}

func (v *Variant) Clear()     { variantClear.Call(uintptr(unsafe.Pointer(v))) }
func (v *Variant) PropClear() { propClear.Call(uintptr(unsafe.Pointer(v))) }

// SafeArray is a SAFEARRAY*; only one-dimensional reads are needed here.
type SafeArray uintptr

func (a SafeArray) Destroy() { safeArrayDelete.Call(uintptr(a)) }

func (a SafeArray) Bounds() (lower, upper int32) {
	safeArrayLower.Call(uintptr(a), 1, uintptr(unsafe.Pointer(&lower)))
	safeArrayUpper.Call(uintptr(a), 1, uintptr(unsafe.Pointer(&upper)))
	return
}

func (a SafeArray) Get(index int32, out unsafe.Pointer) bool {
	hr, _, _ := safeArrayGet.Call(uintptr(a), uintptr(unsafe.Pointer(&index)), uintptr(out))
	return !Failed(hr)
}

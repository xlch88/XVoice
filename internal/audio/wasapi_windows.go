package audio

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/xlch88/XVoice/internal/winapi"
)

var (
	clsidEnumerator = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidEnumerator   = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidAudioClient  = windows.GUID{Data1: 0x1cb9ad4c, Data2: 0xdbfa, Data3: 0x4c32, Data4: [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	iidCapture      = windows.GUID{Data1: 0xc8adbd64, Data2: 0xe71e, Data3: 0x48a0, Data4: [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
	keyFriendlyName = struct {
		fmtid windows.GUID
		pid   uint32
	}{windows.GUID{Data1: 0xa45c254e, Data2: 0xdf1c, Data3: 0x4efd, Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 14}
)

// vtable slots
const (
	enumEndpoints   = 3 // IMMDeviceEnumerator::EnumAudioEndpoints
	enumDefault     = 4 // ::GetDefaultAudioEndpoint
	enumGetDevice   = 5 // ::GetDevice
	collCount       = 3 // IMMDeviceCollection::GetCount
	collItem        = 4 // ::Item
	devActivate     = 3 // IMMDevice::Activate
	devProperties   = 4 // ::OpenPropertyStore
	devID           = 5 // ::GetId
	propGetValue    = 5 // IPropertyStore::GetValue
	clientInit      = 3 // IAudioClient::Initialize
	clientStart     = 10
	clientStop      = 11
	clientService   = 14
	captureGet      = 3 // IAudioCaptureClient::GetBuffer
	captureRelease  = 4
	captureNextSize = 5
)

const eCapture, eConsole, deviceActive = 1, 0, 1

func withEnumerator(f func(e *winapi.Object) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := winapi.InitMTA(); err != nil {
		return err
	}
	defer winapi.Uninit()
	e, err := winapi.Create(&clsidEnumerator, &iidEnumerator)
	if err != nil {
		return err
	}
	defer e.Release()
	return f(e)
}

func deviceInfo(d *winapi.Object) (id, name string) {
	var p *uint16
	if !winapi.Failed(d.Call(devID, uintptr(unsafe.Pointer(&p)))) && p != nil {
		id = winapi.TakeString(p)
	}
	var store *winapi.Object
	if winapi.Failed(d.Call(devProperties, 0, uintptr(unsafe.Pointer(&store)))) || store == nil {
		return id, id
	}
	defer store.Release()
	var v winapi.Variant
	if !winapi.Failed(store.Call(propGetValue, uintptr(unsafe.Pointer(&keyFriendlyName)), uintptr(unsafe.Pointer(&v)))) && v.VT == 31 {
		name = windows.UTF16PtrToString((*uint16)(v.Ptr))
	}
	v.PropClear()
	if name == "" {
		name = id
	}
	return id, name
}

// Devices lists active capture endpoints; the system default is marked.
func Devices() ([]Device, error) {
	var list []Device
	err := withEnumerator(func(e *winapi.Object) error {
		var defaultID string
		var def *winapi.Object
		if !winapi.Failed(e.Call(enumDefault, eCapture, eConsole, uintptr(unsafe.Pointer(&def)))) && def != nil {
			defaultID, _ = deviceInfo(def)
			def.Release()
		}
		var coll *winapi.Object
		if hr := e.Call(enumEndpoints, eCapture, deviceActive, uintptr(unsafe.Pointer(&coll))); winapi.Failed(hr) {
			return winapi.HRError("枚举麦克风", hr)
		}
		defer coll.Release()
		var count uint32
		coll.Call(collCount, uintptr(unsafe.Pointer(&count)))
		for i := uint32(0); i < count; i++ {
			var d *winapi.Object
			if winapi.Failed(coll.Call(collItem, uintptr(i), uintptr(unsafe.Pointer(&d)))) || d == nil {
				continue
			}
			id, name := deviceInfo(d)
			d.Release()
			list = append(list, Device{ID: id, Name: name, Default: id == defaultID})
		}
		return nil
	})
	return list, err
}

// Capture records from deviceID (empty or missing → system default) until ctx ends.
func Capture(ctx context.Context, deviceID string, send func([]byte) error, ready func()) error {
	return pump(ctx, send, func(ctx context.Context, chunks chan<- []byte) error {
		return withEnumerator(func(e *winapi.Object) error {
			return record(ctx, e, deviceID, chunks, ready)
		})
	})
}

type waveFormat struct {
	Tag, Channels              uint16
	SamplesPerSec, BytesPerSec uint32
	BlockAlign, Bits, Extra    uint16
}

func record(ctx context.Context, e *winapi.Object, deviceID string, chunks chan<- []byte, ready func()) error {
	var d *winapi.Object
	if deviceID != "" {
		id, _ := windows.UTF16PtrFromString(deviceID)
		if winapi.Failed(e.Call(enumGetDevice, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&d)))) {
			d = nil // Unplugged: fall back to the default device.
		}
	}
	if d == nil {
		if hr := e.Call(enumDefault, eCapture, eConsole, uintptr(unsafe.Pointer(&d))); winapi.Failed(hr) || d == nil {
			return errors.New("没有可用的麦克风")
		}
	}
	defer d.Release()
	var client *winapi.Object
	if hr := d.Call(devActivate, uintptr(unsafe.Pointer(&iidAudioClient)), 1|4, 0, uintptr(unsafe.Pointer(&client))); winapi.Failed(hr) {
		return winapi.HRError("打开麦克风", hr)
	}
	defer client.Release()
	format := waveFormat{Tag: 1, Channels: 1, SamplesPerSec: 16000, BytesPerSec: 32000, BlockAlign: 2, Bits: 16}
	// AUTOCONVERTPCM | SRC_DEFAULT_QUALITY lets shared mode resample the mix
	// format to 16 kHz mono for us. Buffer: 500 ms in 100 ns units.
	if hr := client.Call(clientInit, 0, 0x80000000|0x08000000, 5_000_000, 0, uintptr(unsafe.Pointer(&format)), 0); winapi.Failed(hr) {
		if uint32(hr) == 0x88890008 {
			return errors.New("麦克风被其他程序独占")
		}
		return winapi.HRError("初始化麦克风", hr)
	}
	var capture *winapi.Object
	if hr := client.Call(clientService, uintptr(unsafe.Pointer(&iidCapture)), uintptr(unsafe.Pointer(&capture))); winapi.Failed(hr) {
		return winapi.HRError("获取采集接口", hr)
	}
	defer capture.Release()
	if hr := client.Call(clientStart); winapi.Failed(hr) {
		return winapi.HRError("启动麦克风", hr)
	}
	defer client.Call(clientStop)
	ready()
	const chunkSize = 1280 // 40 ms
	pending := make([]byte, 0, chunkSize*2)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		for {
			var frames uint32
			if hr := capture.Call(captureNextSize, uintptr(unsafe.Pointer(&frames))); winapi.Failed(hr) {
				if uint32(hr) == 0x88890004 {
					return errors.New("麦克风已断开")
				}
				return winapi.HRError("读取麦克风", hr)
			}
			if frames == 0 {
				break
			}
			var data unsafe.Pointer
			var flags uint32
			if hr := capture.Call(captureGet, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0); winapi.Failed(hr) {
				return winapi.HRError("读取麦克风", hr)
			}
			if flags&2 != 0 || data == nil { // AUDCLNT_BUFFERFLAGS_SILENT
				pending = append(pending, make([]byte, frames*2)...)
			} else {
				pending = append(pending, unsafe.Slice((*byte)(data), frames*2)...)
			}
			capture.Call(captureRelease, uintptr(frames))
		}
		for len(pending) >= chunkSize {
			select {
			case chunks <- append([]byte(nil), pending[:chunkSize]...):
			default:
				return fmt.Errorf("音频发送积压，已停止以避免丢失语音")
			}
			pending = append(pending[:0], pending[chunkSize:]...)
		}
	}
}

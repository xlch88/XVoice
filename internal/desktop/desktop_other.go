//go:build !windows

package desktop

func Start(hotkey string, onHotkey func()) error { return ErrUnsupported }

func SetHotkey(value string) {}

func CaptureHotkey(done func(value string)) {}

func Acquire() (Target, error) { return nil, ErrUnsupported }

func SetAutostart(on bool) error { return ErrUnsupported }

func Close() {}

//go:build windows

package tip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const serverKey = `SOFTWARE\Classes\CLSID\{B7E3A1C4-5D2F-4A8B-9C61-3E7F0D4A2B95}\InprocServer32`

var (
	shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")
	findWindowEx   = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowExW")
)

// root holds every installed copy, one directory per version.
func root() string { return filepath.Join(os.Getenv("ProgramFiles"), "XVoice", "tip") }

// files are where the copy built into this executable is installed, x64 then x86.
func files() [2]string {
	sum := sha256.New()
	sum.Write(dll64)
	sum.Write(dll32)
	dir := filepath.Join(root(), hex.EncodeToString(sum.Sum(nil))[:12])
	return [2]string{filepath.Join(dir, "xvoice_tip_x64.dll"), filepath.Join(dir, "xvoice_tip_x86.dll")}
}

// registered is the DLL one registry view (64- or 32-bit) points the service at.
func registered(view uint32) string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, serverKey, registry.QUERY_VALUE|view)
	if err != nil {
		return ""
	}
	defer key.Close()
	path, _, _ := key.GetStringValue("")
	return path
}

func Check() Status {
	own := files()
	path := registered(registry.WOW64_64KEY)
	s := Status{
		Supported: len(dll64) > 0,
		Installed: path != "",
		Current:   len(dll64) > 0 && strings.EqualFold(path, own[0]) && strings.EqualFold(registered(registry.WOW64_32KEY), own[1]),
	}
	class := windows.StringToUTF16Ptr(WindowClass)
	var window uintptr
	for {
		window, _, _ = findWindowEx.Call(^uintptr(2), window, uintptr(unsafe.Pointer(class)), 0) // HWND_MESSAGE
		if window == 0 {
			return s
		}
		s.Active++
	}
}

// extract writes the built-in copy to its directory. A file already there
// has the same content (the directory is named after it) and may be loaded,
// so it is left alone.
func extract() ([2]string, error) {
	paths := files()
	if len(dll64) == 0 {
		return paths, errors.New("这个版本没有附带输入法服务")
	}
	if err := os.MkdirAll(filepath.Dir(paths[0]), 0o755); err != nil {
		return paths, err
	}
	for i, data := range [][]byte{dll64, dll32} {
		if info, err := os.Stat(paths[i]); err == nil && info.Size() == int64(len(data)) {
			continue
		}
		if err := os.WriteFile(paths[i], data, 0o644); err != nil {
			return paths, err
		}
	}
	return paths, nil
}

// regsvr runs the x64 and x86 regsvr32 on the two DLLs.
func regsvr(paths [2]string, unregister bool) error {
	system := os.Getenv("SystemRoot")
	for i, exe := range []string{filepath.Join(system, "System32", "regsvr32.exe"), filepath.Join(system, "SysWOW64", "regsvr32.exe")} {
		args := []string{"/s", paths[i]}
		if unregister {
			args = append([]string{"/u"}, args...)
		}
		if err := exec.Command(exe, args...).Run(); err != nil {
			return fmt.Errorf("regsvr32 %s：%w", filepath.Base(paths[i]), err)
		}
	}
	return nil
}

// Install registers the built-in copy, replacing whatever copy is running.
// It needs elevation.
func Install() error {
	paths, err := extract()
	if err != nil {
		return err
	}
	// Unregistering first makes TSF deactivate the running copy everywhere;
	// the pause lets that reach every application before the new copy is
	// registered and loaded in its place. Not being registered yet is fine.
	regsvr(paths, true)
	time.Sleep(time.Second)
	if err := regsvr(paths, false); err != nil {
		return err
	}
	removeOthers(filepath.Dir(paths[0]))
	return nil
}

// Uninstall unregisters the service and removes every installed copy. It
// needs elevation.
func Uninstall() error {
	// Any copy unregisters the service; prefer the registered one.
	paths := [2]string{registered(registry.WOW64_64KEY), registered(registry.WOW64_32KEY)}
	_, missing64 := os.Stat(paths[0])
	_, missing32 := os.Stat(paths[1])
	if missing64 != nil || missing32 != nil {
		var err error
		if paths, err = extract(); err != nil {
			return err
		}
	}
	if err := regsvr(paths, true); err != nil {
		return err
	}
	removeOthers("")
	return nil
}

// removeOthers deletes every installed copy except the one in keep. Copies
// still loaded in running applications are deleted at the next restart.
func removeOthers(keep string) {
	dirs, _ := os.ReadDir(root())
	for _, entry := range dirs {
		dir := filepath.Join(root(), entry.Name())
		if strings.EqualFold(dir, keep) || os.RemoveAll(dir) == nil {
			continue
		}
		left, _ := os.ReadDir(dir)
		for _, file := range left {
			windows.MoveFileEx(windows.StringToUTF16Ptr(filepath.Join(dir, file.Name())), nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		}
		windows.MoveFileEx(windows.StringToUTF16Ptr(dir), nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
	}
	if keep == "" {
		for _, dir := range []string{root(), filepath.Dir(root())} {
			if os.Remove(dir) != nil {
				windows.MoveFileEx(windows.StringToUTF16Ptr(dir), nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
			}
		}
	}
}

// Elevate runs this executable with arg as administrator, which Windows
// confirms with the user, and waits for it to finish.
func Elevate(arg string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	info := struct {
		Size, Mask                        uint32
		Window                            uintptr
		Verb, File, Parameters, Directory *uint16
		Show                              int32
		Instance, IDList                  uintptr
		Class                             *uint16
		ClassKey                          uintptr
		HotKey                            uint32
		Icon                              uintptr
		Process                           windows.Handle
	}{
		Mask:       0x40 | 0x100, // SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC
		Verb:       windows.StringToUTF16Ptr("runas"),
		File:       windows.StringToUTF16Ptr(exe),
		Parameters: windows.StringToUTF16Ptr(arg),
	}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, err := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if err == windows.ERROR_CANCELLED {
			return errors.New("已取消")
		}
		return err
	}
	defer windows.CloseHandle(info.Process)
	windows.WaitForSingleObject(info.Process, windows.INFINITE)
	var code uint32
	windows.GetExitCodeProcess(info.Process, &code)
	if code != 0 {
		return fmt.Errorf("没有完成（代码 %d）", code)
	}
	return nil
}

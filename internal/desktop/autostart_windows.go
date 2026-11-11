package desktop

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// SetAutostart adds this executable to, or removes it from, the programs
// Windows starts when the user signs in.
func SetAutostart(on bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !on {
		if err = key.DeleteValue("XVoice"); err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue("XVoice", `"`+exe+`"`)
}

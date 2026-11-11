// Package config holds the user's settings and persists them as JSON under
// the OS config directory.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Choice is one value a setting can take, with its display name. The first
// choice of every setting has Value "" and is the default, so the zero Config
// is the default configuration and old files keep working as settings grow.
type Choice struct{ Value, Name string }

// Config stores the chosen Value of each setting; the package that implements
// a setting also lists its choices.
type Config struct {
	Microphone  string `json:"microphone,omitempty"`  // Device ID; empty is the system default.
	Language    string `json:"language,omitempty"`    // asr.Languages
	Domain      string `json:"domain,omitempty"`      // asr.Domains
	Typing      string `json:"typing,omitempty"`      // session.Typings
	Punctuation string `json:"punctuation,omitempty"` // session.Punctuations
	AutoStop    string `json:"auto_stop,omitempty"`   // session.AutoStops
	Hotkey      string `json:"hotkey,omitempty"`      // desktop.Hotkeys
	Sound       string `json:"sound,omitempty"`       // "" plays chimes, "off" is silent.
	Autostart   string `json:"autostart,omitempty"`   // "on" launches at sign-in.
}

var mu sync.Mutex

func path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "xvoice", "config.json"), nil
}

// Load returns defaults when the file does not exist yet.
func Load() (Config, error) {
	mu.Lock()
	defer mu.Unlock()
	var c Config
	p, err := path()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(data, &c)
}

func Save(c Config) error {
	mu.Lock()
	defer mu.Unlock()
	p, err := path()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

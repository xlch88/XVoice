// Command xvoice dictates into whatever text field has the focus.
//
// The icon and the Windows manifest (visual styles, per-monitor DPI) are
// compiled into rsrc_windows_*.syso; regenerate them after changing the logo.
//
//go:generate go -C ../../tools run ./genicon ../assets/logo.svg ../assets/icon.png
//go:generate go -C ../../tools tool go-winres simply --arch amd64,arm64 --icon ../assets/icon.png --manifest gui --file-description XVoice --product-name XVoice --out ../cmd/xvoice/rsrc
package main

import (
	"context"
	"os"
	"slices"
	"sync"

	"xvoice/internal/asr"
	"xvoice/internal/audio"
	"xvoice/internal/config"
	"xvoice/internal/desktop"
	"xvoice/internal/session"
	"xvoice/internal/ui"
)

// version is set by build.sh.
var version = "dev"

func main() {
	if !ui.SingleInstance() {
		return
	}
	// A missing or unreadable file means default settings.
	cfg, _ := config.Load()
	var mu sync.Mutex // Guards cfg: the UI thread writes it, dictations read it.
	controller := &session.Controller{
		Acquire: desktop.Acquire,
		Config: func() config.Config {
			mu.Lock()
			defer mu.Unlock()
			return cfg
		},
		OnState: ui.Update,
	}
	if err := desktop.Start(cfg.Hotkey, controller.Toggle); err != nil {
		ui.Alert("初始化失败：" + err.Error())
		os.Exit(1)
	}
	defer desktop.Close()
	if cfg.Autostart == "on" {
		desktop.SetAutostart(true) // The executable may have moved since it was registered.
	}
	err := ui.Run(ui.Options{
		Version: version,
		Config:  cfg,
		OnChange: func(next config.Config) {
			mu.Lock()
			previous := cfg
			cfg = next
			mu.Unlock()
			config.Save(next)
			if next.Hotkey != previous.Hotkey {
				desktop.SetHotkey(next.Hotkey)
			}
			if next.Autostart != previous.Autostart {
				desktop.SetAutostart(next.Autostart == "on")
			}
		},
		Toggle:  controller.Toggle,
		Stop:    controller.Stop,
		Devices: audio.Devices,
		Test: func(ctx context.Context, cfg config.Config, onText func(string), onLevel func(float64)) error {
			return asr.Recognize(ctx, asr.Options{Language: cfg.Language, Domain: cfg.Domain}, func(ctx context.Context, send func([]byte) error, ready func()) error {
				return audio.Capture(ctx, cfg.Microphone, func(pcm []byte) error {
					onLevel(audio.Level(pcm))
					return send(pcm)
				}, ready)
			}, func(text string, _ int) { onText(session.Format(text, cfg.Punctuation)) }, func() {})
		},
		ShowSettings: slices.Contains(os.Args[1:], "--settings"),
	})
	if err != nil {
		ui.Alert(err.Error())
		os.Exit(1)
	}
}

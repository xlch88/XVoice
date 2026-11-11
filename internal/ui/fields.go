package ui

import (
	"xvoice/internal/asr"
	"xvoice/internal/audio"
	"xvoice/internal/config"
	"xvoice/internal/session"
)

// field is one row of the settings form: a label and a drop-down list. Every
// platform renders this same table with one generic loop, so adding a setting
// never touches platform code.
type field struct {
	section string // Heading shown above this row.
	hotkey  bool   // A button that records a key combination, instead of a list.
	label   string
	choices []config.Choice
	// load fetches choices that are slow to get or change at runtime; it runs
	// off the UI thread and its result replaces choices.
	load    func() ([]config.Choice, error)
	missing string                       // Name for a stored value that is not among the choices.
	value   func(*config.Config) *string // The Config field this row edits.
	enabled func(*config.Config) bool    // nil: always.
}

// options returns the choices to list for a stored value, which stays
// selectable even when it is not (or no longer) offered: an unplugged
// microphone, a hand-written hotkey.
func (f field) options(choices []config.Choice, value string) (list []config.Choice, selected int) {
	for i, c := range choices {
		if c.Value == value {
			return choices, i
		}
	}
	name := f.missing
	if name == "" {
		name = value
	}
	return append(choices[:len(choices):len(choices)], config.Choice{Value: value, Name: name}), len(choices)
}

func settingsFields(devices func() ([]audio.Device, error)) []field {
	return []field{
		{
			section: "识别", label: "麦克风",
			choices: []config.Choice{{Name: "系统默认麦克风"}},
			load: func() ([]config.Choice, error) {
				list, err := devices()
				choices := []config.Choice{{Name: "系统默认麦克风"}}
				for _, d := range list {
					if d.Default {
						d.Name += "（当前默认）"
					}
					choices = append(choices, config.Choice{Value: d.ID, Name: d.Name})
				}
				return choices, err
			},
			missing: "已保存的麦克风（未连接）",
			value:   func(c *config.Config) *string { return &c.Microphone },
		},
		{label: "语言", choices: asr.Languages, value: func(c *config.Config) *string { return &c.Language }},
		{
			label: "领域优化", choices: asr.Domains,
			value:   func(c *config.Config) *string { return &c.Domain },
			enabled: func(c *config.Config) bool { return c.Language == "" },
		},
		{section: "输入", label: "上屏方式", choices: session.Typings, value: func(c *config.Config) *string { return &c.Typing }},
		{label: "标点", choices: session.Punctuations, value: func(c *config.Config) *string { return &c.Punctuation }},
		{label: "自动结束", choices: session.AutoStops, value: func(c *config.Config) *string { return &c.AutoStop }},
		{section: "常规", label: "快捷键", hotkey: true, value: func(c *config.Config) *string { return &c.Hotkey }},
		{
			label:   "提示音",
			choices: []config.Choice{{Name: "开启"}, {Value: "off", Name: "关闭"}},
			value:   func(c *config.Config) *string { return &c.Sound },
		},
		{
			label:   "开机启动",
			choices: []config.Choice{{Name: "关闭"}, {Value: "on", Name: "开启"}},
			value:   func(c *config.Config) *string { return &c.Autostart },
		},
	}
}

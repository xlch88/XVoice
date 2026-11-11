package desktop

import "testing"

func TestParseHotkey(t *testing.T) {
	for value, want := range map[string]uint32{
		"":               chordCtrl | chordAlt | 0x20,
		"ctrl+alt+space": chordCtrl | chordAlt | 0x20,
		"Win+Alt+H":      chordWin | chordAlt | 'H',
		"ctrl+shift+f12": chordCtrl | chordShift | 0x7b,
		"f9":             0x78,
		"alt+1":          chordAlt | '1',
	} {
		if got, ok := parseHotkey(value); !ok || got != want {
			t.Errorf("parseHotkey(%q) = %#x, %v; want %#x", value, got, ok, want)
		}
	}
	for _, value := range []string{"ctrl+alt", "ctrl+a+b", "f25", "ctrl+enter"} {
		if got, ok := parseHotkey(value); ok {
			t.Errorf("parseHotkey(%q) = %#x, want failure", value, got)
		}
	}
	// Every offered choice must parse.
	for _, h := range Hotkeys {
		if _, ok := parseHotkey(h.Value); !ok {
			t.Errorf("offered hotkey %q does not parse", h.Value)
		}
	}
}

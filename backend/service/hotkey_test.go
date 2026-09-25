//go:build windows

package service

import "testing"

// The combos arrive as text the user typed into a settings field, so the parser
// is a trust boundary: anything it lets through is handed straight to
// RegisterHotKey, which claims the combination from the whole system.
func TestParseHotkey(t *testing.T) {
	ok := []struct {
		combo string
		mod   uintptr
		vk    uintptr
	}{
		{"Ctrl+Shift+V", modNoRepeat | modControl | modShift, 'V'},
		{"ctrl+shift+b", modNoRepeat | modControl | modShift, 'B'},
		{"Alt+F4", modNoRepeat | modAlt, 0x73},
		{"Ctrl+Alt+9", modNoRepeat | modControl | modAlt, '9'},
		{"Win+Shift+F12", modNoRepeat | modWin | modShift, 0x7B},
	}
	for _, c := range ok {
		mod, vk, valid := parseHotkey(c.combo)
		if !valid || mod != c.mod || vk != c.vk {
			t.Errorf("parseHotkey(%q) = %#x, %#x, %v; want %#x, %#x, true", c.combo, mod, vk, valid, c.mod, c.vk)
		}
	}

	// "V" alone would take the letter away from every other program on the
	// machine; the rest are simply not shortcuts.
	for _, combo := range []string{"", "V", "Ctrl", "Ctrl+", "Ctrl+Shift", "Ctrl+F13", "Ctrl+Hyper+V", "Ctrl+Space"} {
		if _, _, valid := parseHotkey(combo); valid {
			t.Errorf("parseHotkey(%q) accepted an unusable combination", combo)
		}
	}
}

// A settings object that predates these fields must not read as "everything
// off": systemProxy has always defaulted to on in the interface, and a tray
// checkbox contradicting the window is worse than no checkbox.
func TestTrayTogglesDefaults(t *testing.T) {
	empty := trayTogglesFrom(map[string]interface{}{})
	if empty.KillSwitch || empty.TunMode || !empty.SystemProxy {
		t.Errorf("defaults = %+v; want only SystemProxy on", empty)
	}

	set := trayTogglesFrom(map[string]interface{}{"killSwitch": true, "systemProxy": false})
	if !set.KillSwitch || set.TunMode || set.SystemProxy {
		t.Errorf("explicit values = %+v", set)
	}
}

func TestFormatTrayBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"}
	for in, want := range cases {
		if got := formatTrayBytes(in); got != want {
			t.Errorf("formatTrayBytes(%d) = %q; want %q", in, got, want)
		}
	}
}

package service

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// The tray toggle offers "hide" or "show" purely from windowVisible, so that
// flag has to follow the window from whichever side moved it. It used not to:
// only the frontend's own hide/show calls updated it, while BringToFront — the
// path the global hotkey and a server picked from the tray both take — showed
// the window and said nothing. The menu was then left offering to hide a window
// that was already on screen, or to show one that was already gone, and the
// first click on it did nothing visible.
func TestWindowVisibilityFollowsBothDirections(t *testing.T) {
	s := &AppService{}

	s.SetWindowVisible(true)
	s.NotifyWindowHidden()
	if s.isWindowVisible() {
		t.Fatal("window still marked visible after NotifyWindowHidden")
	}

	// What BringToFront does once the window is actually back; the runtime half
	// needs a live Wails context, this half does not.
	s.markWindowShown()
	if !s.isWindowVisible() {
		t.Fatal("window still marked hidden after it was brought to the front")
	}
}

// Лимит szTip Windows считается в единицах UTF-16. Имена узлов в подписках
// сплошь и рядом несут флаговые эмодзи по две единицы каждое, и обрезка по
// рунам выпускала строку вдвое длиннее лимита — systray копирует её в
// [128]uint16, и переполнение оставляет массив без завершающего нуля.
func TestClipTrayNameBudgetsUTF16Units(t *testing.T) {
	units := func(s string) int {
		n := 0
		for _, r := range s {
			n += utf16.RuneLen(r)
		}
		return n
	}

	cases := map[string]string{
		"ascii":      strings.Repeat("a", 200),
		"cyrillic":   strings.Repeat("я", 200),
		"flags":      strings.Repeat("🇳🇱", 100),
		"mixed":      strings.Repeat("NL 🇳🇱 ", 40),
		"emoji-tail": strings.Repeat("a", 59) + strings.Repeat("😀", 10),
	}
	for name, input := range cases {
		got := clipTrayName(input)
		// 60 единиц бюджета плюс сам многоточие-символ.
		if units(got) > 61 {
			t.Errorf("%s: clipped name is %d UTF-16 units (%q), over the budget", name, units(got), got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: clipping split a rune: %q", name, got)
		}
	}

	// Короткое имя возвращается как есть, без многоточия.
	if got := clipTrayName("Amsterdam 01"); got != "Amsterdam 01" {
		t.Errorf("short name was altered: %q", got)
	}
}

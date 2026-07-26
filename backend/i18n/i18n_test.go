package i18n

import (
	"strings"
	"testing"
)

// A key present in one language and missing from the other silently falls back,
// which is what "half-translated interface" bug reports actually are. Nothing
// else catches it, so this test does.
func TestLanguageTablesHaveTheSameKeys(t *testing.T) {
	for lang, table := range messages {
		for other, otherTable := range messages {
			if lang == other {
				continue
			}
			for key := range table {
				if _, ok := otherTable[key]; !ok {
					t.Errorf("key %q exists in %s but is missing from %s", key, lang, other)
				}
			}
		}
	}
}

// A format verb in one language and not the other means one of them renders
// with a stray "%!s(MISSING)".
func TestFormatVerbsMatchAcrossLanguages(t *testing.T) {
	countVerbs := func(s string) int {
		n := 0
		for i := 0; i+1 < len(s); i++ {
			if s[i] != '%' {
				continue
			}
			if s[i+1] == '%' {
				i++
				continue
			}
			n++
		}
		return n
	}

	for key, ru := range messages[RU] {
		en, ok := messages[EN][key]
		if !ok {
			continue // reported by TestLanguageTablesHaveTheSameKeys
		}
		if got, want := countVerbs(en), countVerbs(ru); got != want {
			t.Errorf("key %q has %d format verbs in RU but %d in EN\n  RU: %s\n  EN: %s",
				key, want, got, ru, en)
		}
	}
}

func TestTranslationsAreNotEmpty(t *testing.T) {
	for lang, table := range messages {
		for key, value := range table {
			if strings.TrimSpace(value) == "" {
				t.Errorf("key %q is empty in %s", key, lang)
			}
		}
	}
}

func TestSetLanguage(t *testing.T) {
	t.Cleanup(func() { SetLanguage(string(defaultLang)) })

	SetLanguage("EN")
	if Language() != EN {
		t.Errorf("Language() = %q, want EN", Language())
	}
	if got := T(TrayQuit); got != "Quit" {
		t.Errorf("T(TrayQuit) = %q, want %q", got, "Quit")
	}

	// Case and surrounding space come from a hand-editable settings file.
	SetLanguage("  ru  ")
	if Language() != RU {
		t.Errorf("Language() = %q, want RU after a padded lowercase code", Language())
	}

	// An unknown code must leave the selection alone rather than blanking the
	// interface, since settings.json can be edited by hand.
	SetLanguage("KLINGON")
	if Language() != RU {
		t.Errorf("an unknown code changed the language to %q", Language())
	}
	SetLanguage("")
	if Language() != RU {
		t.Errorf("an empty code changed the language to %q", Language())
	}
}

func TestTFormatsArguments(t *testing.T) {
	t.Cleanup(func() { SetLanguage(string(defaultLang)) })
	SetLanguage("EN")

	got := T(DiagProxyPortName, 20809)
	if got != "Proxy port (20809)" {
		t.Errorf("T(DiagProxyPortName, 20809) = %q", got)
	}
}

// An unknown id must surface as itself, so a typo is visible in the interface
// instead of rendering as an empty label.
func TestTUnknownIDReturnsTheID(t *testing.T) {
	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("T on an unknown id = %q, want the id back", got)
	}
}

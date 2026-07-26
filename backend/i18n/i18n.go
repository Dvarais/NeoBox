// Package i18n translates the user-facing text the backend produces.
//
// Most of NeoBox's interface is translated in the frontend (frontend/modules/
// translations.js), but three things are rendered by Go and never pass through
// it: the system tray menu, Windows toast notifications, and the diagnostics
// screen. Those were hard-coded in Russian, so switching the app to English left
// them Russian — and error strings reached the user half-translated, an English
// wrapper around a Russian cause.
//
// Rather than push message IDs to the frontend and change the binding contract,
// the backend renders in the user's language directly: it already reads
// settings.json, where the choice lives.
package i18n

import (
	"fmt"
	"strings"
	"sync"
)

// Lang is a supported interface language. The values match the "language" field
// written by the frontend into settings.json.
type Lang string

const (
	RU Lang = "RU"
	EN Lang = "EN"
)

// defaultLang is used before settings are read and for any message missing a
// translation. Russian is NeoBox's primary language.
const defaultLang = RU

var (
	mu      sync.RWMutex
	current = defaultLang
)

// SetLanguage selects the language used by T. Unknown codes leave the current
// selection untouched, so a corrupt settings file cannot blank the interface.
func SetLanguage(code string) {
	lang := Lang(strings.ToUpper(strings.TrimSpace(code)))
	if _, ok := messages[lang]; !ok {
		return
	}
	mu.Lock()
	current = lang
	mu.Unlock()
}

// Language reports the language currently in effect.
func Language() Lang {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// T returns the message for id in the current language, formatted with args
// when any are supplied.
//
// A message missing from the current language falls back to the default one; a
// message missing everywhere returns the id itself, so a typo shows up in the
// interface as an obviously wrong string instead of an empty label.
func T(id string, args ...interface{}) string {
	lang := Language()

	format, ok := messages[lang][id]
	if !ok {
		format, ok = messages[defaultLang][id]
	}
	if !ok {
		format = id
	}

	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

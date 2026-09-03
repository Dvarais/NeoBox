package service

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"NeoBox/backend/security"
	"NeoBox/backend/storage"
)

// The credentials these tests look for on disk. Any of these appearing in
// settings.json is the exact leak the split exists to prevent.
const (
	testSelectedLink = "vless://11111111-2222-3333-4444-555555555555@node.example.com:443#selected"
	testFavouriteA   = "trojan://s3cr3t-password@fav-a.example.org:443#fav-a"
	testFavouriteB   = "ss://YWVzLTI1Ni1nY206cGFzcw@fav-b.example.net:8388#fav-b"
)

func newSettingsService(t *testing.T) *AppService {
	t.Helper()
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init failed: %v", err)
	}
	return &AppService{userDataDir: dir}
}

// readRaw returns the bytes on disk, failing the test if the file is missing.
func readRaw(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", filepath.Base(path), err)
	}
	return data
}

// assertNoCredentials fails if any test credential appears anywhere in data.
// It searches the raw bytes rather than parsed fields on purpose: a leak through
// an unexpected key, a nested object or a stray copy would still be a leak.
func assertNoCredentials(t *testing.T, what string, data []byte) {
	t.Helper()
	for _, secret := range []string{testSelectedLink, testFavouriteA, testFavouriteB} {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("%s contains a plaintext proxy link: %s", what, secret)
		}
	}
	// The UUID and passwords on their own, in case a link is ever stored split
	// into parts.
	for _, fragment := range []string{"11111111-2222-3333-4444-555555555555", "s3cr3t-password"} {
		if bytes.Contains(data, []byte(fragment)) {
			t.Errorf("%s contains plaintext credential material: %s", what, fragment)
		}
	}
}

func settingsWith(selected string, favourites []string) string {
	payload := map[string]interface{}{
		"language":           "EN",
		"tunMode":            true,
		"dns":                "1.1.1.1",
		"bypassRu":           true,
		"lastSelectedServer": selected,
		"favoriteLinks":      favourites,
	}
	out, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// SaveSettings must keep credentials out of the plaintext file while still
// returning them from GetSettings, so the frontend sees no difference.
func TestSaveSettingsKeepsCredentialsOutOfPlaintext(t *testing.T) {
	s := newSettingsService(t)

	if !s.SaveSettings(settingsWith(testSelectedLink, []string{testFavouriteA, testFavouriteB})) {
		t.Fatal("SaveSettings returned false")
	}

	plain := readRaw(t, s.settingsPath())
	assertNoCredentials(t, "settings.json", plain)

	// The non-secret half must still be there and still be human-readable.
	var onDisk map[string]interface{}
	if err := json.Unmarshal(plain, &onDisk); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	if onDisk["dns"] != "1.1.1.1" || onDisk["tunMode"] != true {
		t.Errorf("plain settings lost fields: %v", onDisk)
	}
	for _, key := range secretSettingKeys {
		if _, present := onDisk[key]; present {
			t.Errorf("settings.json still carries the secret key %q", key)
		}
	}
	if !bytes.Contains(plain, []byte("\n  ")) {
		t.Error("settings.json is not pretty-printed; it is meant to be hand-editable")
	}

	// state.json must exist and be an encrypted envelope, not readable JSON.
	sealed := readRaw(t, s.statePath())
	assertNoCredentials(t, "state.json", sealed)
	if json.Valid(sealed) {
		t.Error("state.json is readable JSON — it should be an encrypted envelope")
	}
}

// The split must be invisible to callers: what goes into SaveSettings comes back
// out of GetSettings unchanged.
func TestSettingsRoundTrip(t *testing.T) {
	s := newSettingsService(t)
	favourites := []string{testFavouriteA, testFavouriteB}

	if !s.SaveSettings(settingsWith(testSelectedLink, favourites)) {
		t.Fatal("SaveSettings returned false")
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &got); err != nil {
		t.Fatalf("GetSettings did not return valid JSON: %v", err)
	}

	if got["lastSelectedServer"] != testSelectedLink {
		t.Errorf("lastSelectedServer = %v, want %s", got["lastSelectedServer"], testSelectedLink)
	}
	gotFavs, ok := got["favoriteLinks"].([]interface{})
	if !ok || len(gotFavs) != len(favourites) {
		t.Fatalf("favoriteLinks = %v, want %d entries", got["favoriteLinks"], len(favourites))
	}
	for i, want := range favourites {
		if gotFavs[i] != want {
			t.Errorf("favoriteLinks[%d] = %v, want %s", i, gotFavs[i], want)
		}
	}
	if got["dns"] != "1.1.1.1" || got["language"] != "EN" {
		t.Errorf("plain fields did not survive the round trip: %v", got)
	}
}

// Поле «домены для прямого доступа» снято, но домены из него — это маршруты,
// которые человек завёл руками. Миграция обязана перенести их в правила, и
// перенести ПЕРЕД теми, что уже есть: в конфиге старое поле шло раньше правил.
func TestMigrateCustomDirectFoldsDomainsIntoRules(t *testing.T) {
	s := newSettingsService(t)

	legacy := map[string]interface{}{
		"tunMode":      true,
		"customDirect": []string{"example.com", "  spaced.example  ", ""},
		"customRules": []map[string]string{
			{"action": "block", "type": "domain", "value": "ads.example"},
		},
		"profiles": []map[string]interface{}{
			{"id": "p1", "name": "дома", "server": nil, "settings": map[string]interface{}{
				"customDirect": []string{"profile.example"},
			}},
		},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("failed to encode legacy settings: %v", err)
	}
	if !s.SaveSettings(string(raw)) {
		t.Fatal("SaveSettings returned false")
	}

	s.migrateCustomDirect()
	s.migrateCustomDirect() // повторный старт не должен ничего удвоить

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &got); err != nil {
		t.Fatalf("GetSettings did not return valid JSON: %v", err)
	}
	if _, present := got["customDirect"]; present {
		t.Error("customDirect survived the migration")
	}

	rules, _ := got["customRules"].([]interface{})
	want := []map[string]interface{}{
		{"action": "direct", "type": "domain_suffix", "value": "example.com"},
		{"action": "direct", "type": "domain_suffix", "value": "spaced.example"},
		{"action": "block", "type": "domain", "value": "ads.example"},
	}
	if len(rules) != len(want) {
		t.Fatalf("customRules = %v, want %d rules", rules, len(want))
	}
	for i, expected := range want {
		rule, _ := rules[i].(map[string]interface{})
		for key, value := range expected {
			if rule[key] != value {
				t.Errorf("rule %d: %s = %v, want %v", i, key, rule[key], value)
			}
		}
	}

	profiles, _ := got["profiles"].([]interface{})
	if len(profiles) != 1 {
		t.Fatalf("profiles = %v, want the one that was saved", profiles)
	}
	profile, _ := profiles[0].(map[string]interface{})
	nested, _ := profile["settings"].(map[string]interface{})
	if _, present := nested["customDirect"]; present {
		t.Error("customDirect survived the migration inside a profile")
	}
	profileRules, _ := nested["customRules"].([]interface{})
	if len(profileRules) != 1 {
		t.Fatalf("profile customRules = %v, want the folded domain", profileRules)
	}
	if rule, _ := profileRules[0].(map[string]interface{}); rule["value"] != "profile.example" {
		t.Errorf("profile rule = %v, want profile.example routed direct", rule)
	}
}

// A settings.json written by a build from before the split must be migrated on
// startup, not left sitting in the clear until the user happens to save.
func TestMigrateSecretSettingsMovesLegacyPlaintext(t *testing.T) {
	s := newSettingsService(t)

	legacy := settingsWith(testSelectedLink, []string{testFavouriteA})
	if err := os.WriteFile(s.settingsPath(), []byte(legacy), 0644); err != nil {
		t.Fatalf("failed to seed legacy settings.json: %v", err)
	}

	s.migrateSecretSettings()

	assertNoCredentials(t, "settings.json after migration", readRaw(t, s.settingsPath()))
	assertNoCredentials(t, "state.json after migration", readRaw(t, s.statePath()))

	// Migrating must not lose the values — only move them.
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &got); err != nil {
		t.Fatalf("GetSettings did not return valid JSON: %v", err)
	}
	if got["lastSelectedServer"] != testSelectedLink {
		t.Errorf("migration lost lastSelectedServer: %v", got["lastSelectedServer"])
	}
	if got["tunMode"] != true {
		t.Errorf("migration lost plain settings: %v", got)
	}
}

// Migration runs on every startup. On a settings file that has already been
// split it must do nothing at all, and must not clobber the encrypted half.
func TestMigrateSecretSettingsIsIdempotent(t *testing.T) {
	s := newSettingsService(t)
	if !s.SaveSettings(settingsWith(testSelectedLink, []string{testFavouriteA})) {
		t.Fatal("SaveSettings returned false")
	}

	before := readRaw(t, s.statePath())
	s.migrateSecretSettings()
	s.migrateSecretSettings()
	after := readRaw(t, s.statePath())

	if !bytes.Equal(before, after) {
		t.Error("migration rewrote state.json when there was nothing to migrate")
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &got); err != nil {
		t.Fatalf("GetSettings did not return valid JSON: %v", err)
	}
	if got["lastSelectedServer"] != testSelectedLink {
		t.Errorf("repeated migration lost lastSelectedServer: %v", got["lastSelectedServer"])
	}
}

// If a user pastes a credential back into settings.json by hand while the
// encrypted file already holds one, the encrypted file is the newer copy and
// must win — and the hand-added plaintext must not survive.
func TestMigrationPrefersEncryptedCopyOverStalePlaintext(t *testing.T) {
	s := newSettingsService(t)
	if !s.SaveSettings(settingsWith(testSelectedLink, []string{testFavouriteA})) {
		t.Fatal("SaveSettings returned false")
	}

	plain := s.readPlainSettingsLocked()
	plain["lastSelectedServer"] = testFavouriteB // stale value, added by hand
	if err := s.writePlainSettingsLocked(plain); err != nil {
		t.Fatalf("failed to rewrite settings.json: %v", err)
	}

	s.migrateSecretSettings()

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &got); err != nil {
		t.Fatalf("GetSettings did not return valid JSON: %v", err)
	}
	if got["lastSelectedServer"] != testSelectedLink {
		t.Errorf("stale plaintext won over the encrypted copy: %v", got["lastSelectedServer"])
	}
	assertNoCredentials(t, "settings.json after migration", readRaw(t, s.settingsPath()))
}

// Clearing every favourite must clear the stored copy too. Skipping the write
// when there is nothing to store would leave the previous file in place and
// resurrect the credentials on the next launch.
func TestClearingCredentialsClearsStoredCopy(t *testing.T) {
	s := newSettingsService(t)
	if !s.SaveSettings(settingsWith(testSelectedLink, []string{testFavouriteA, testFavouriteB})) {
		t.Fatal("SaveSettings returned false")
	}
	if !s.SaveSettings(`{"tunMode":true,"favoriteLinks":[]}`) {
		t.Fatal("second SaveSettings returned false")
	}

	assertNoCredentials(t, "state.json after clearing", readRaw(t, s.statePath()))

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &got); err != nil {
		t.Fatalf("GetSettings did not return valid JSON: %v", err)
	}
	if _, present := got["lastSelectedServer"]; present {
		t.Errorf("lastSelectedServer survived a save that omitted it: %v", got["lastSelectedServer"])
	}
	if favs, ok := got["favoriteLinks"].([]interface{}); !ok || len(favs) != 0 {
		t.Errorf("favoriteLinks = %v, want an empty list", got["favoriteLinks"])
	}
}

// A fresh install has neither file. GetSettings must return an empty object
// rather than failing, and must not create anything on the read path.
func TestGetSettingsOnFreshInstall(t *testing.T) {
	s := newSettingsService(t)

	if got := s.GetSettings(); got != "{}" {
		t.Errorf("GetSettings() = %s, want {}", got)
	}
	if _, err := os.Stat(s.settingsPath()); !os.IsNotExist(err) {
		t.Error("GetSettings created settings.json; it must be a pure read")
	}
	if _, err := os.Stat(s.statePath()); !os.IsNotExist(err) {
		t.Error("GetSettings created state.json; it must be a pure read")
	}
}

// A corrupt or hand-mangled settings.json must not take the credentials down
// with it: the encrypted half is a separate file and still readable.
func TestCorruptPlainSettingsDoesNotLoseCredentials(t *testing.T) {
	s := newSettingsService(t)
	if !s.SaveSettings(settingsWith(testSelectedLink, []string{testFavouriteA})) {
		t.Fatal("SaveSettings returned false")
	}
	if err := os.WriteFile(s.settingsPath(), []byte("{not json at all"), 0644); err != nil {
		t.Fatalf("failed to corrupt settings.json: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &got); err != nil {
		t.Fatalf("GetSettings did not return valid JSON: %v", err)
	}
	if got["lastSelectedServer"] != testSelectedLink {
		t.Errorf("a corrupt settings.json lost the stored server: %v", got["lastSelectedServer"])
	}
}

// SaveSettings must reject anything that is not a JSON object, rather than
// writing it out and corrupting both files.
func TestSaveSettingsRejectsNonObjects(t *testing.T) {
	s := newSettingsService(t)
	for _, payload := range []string{``, `not json`, `[1,2,3]`, `"a string"`, `null`} {
		if s.SaveSettings(payload) {
			t.Errorf("SaveSettings(%q) = true, want false", payload)
		}
		if _, err := os.Stat(s.settingsPath()); !os.IsNotExist(err) {
			t.Fatalf("SaveSettings(%q) wrote settings.json despite invalid input", payload)
		}
	}
}

// Saving a language change used to deadlock: SaveSettings held fileMu and called
// applyLanguage, which rebuilds the tray, and the rebuild takes fileMu itself to
// read the subscriptions. sync.Mutex is not reentrant, so the app froze on every
// language switch. The point of this test is that it TERMINATES.
func TestSaveSettingsWithLanguageChangeDoesNotDeadlock(t *testing.T) {
	s := newSettingsService(t)

	// Seed subscriptions so the tray rebuild actually reaches the file read that
	// contends for fileMu, rather than bailing out early on an absent file.
	subs := []Subscription{{
		ID:    "sub-1",
		Name:  "Test",
		Links: []string{testFavouriteA},
	}}
	seed, err := json.Marshal(subs)
	if err != nil {
		t.Fatalf("failed to marshal seed: %v", err)
	}
	if !s.SaveSubscriptions(string(seed)) {
		t.Fatal("seeding SaveSubscriptions failed")
	}

	done := make(chan bool, 1)
	go func() {
		// Two different languages, so applyLanguage cannot short-circuit on
		// "already in this language" and skip the tray rebuild entirely.
		okRU := s.SaveSettings(`{"language":"RU","tunMode":true}`)
		okEN := s.SaveSettings(`{"language":"EN","tunMode":true}`)
		done <- okRU && okEN
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Error("SaveSettings reported failure on a language change")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SaveSettings deadlocked on a language change")
	}
}

// Guards the intent of the split: the list must not quietly grow to cover
// fields users are meant to edit by hand in settings.json.
//
// The guard used to be a substring test — a key had to contain "server" or
// "link" — and that broke the moment a legitimately secret field arrived under
// a name that says nothing about links: "profiles", where a saved profile may
// carry the server it was made with. The heuristic was only ever a stand-in for
// the real rule, which is a judgement about content, so the list is now spelled
// out here with the reason each entry earns its place. Adding a field to
// secretSettingKeys now costs one line in this test, and that is the point: it
// cannot happen without somebody saying out loud why.
func TestSecretSettingKeysAreCredentialFields(t *testing.T) {
	reasons := map[string]string{
		"lastSelectedServer": "полная ссылка на прокси",
		"favoriteLinks":      "список полных ссылок на прокси",
		"profiles":           "профиль может нести ссылку на сервер, с которым его сохранили",
	}

	for _, key := range secretSettingKeys {
		if _, declared := reasons[key]; !declared {
			t.Errorf("secretSettingKeys contains %q, which is not declared in this test — "+
				"add it here together with the reason it must be encrypted", key)
		}
	}
	for key := range reasons {
		if !slices.Contains(secretSettingKeys, key) {
			t.Errorf("%q is declared here but no longer in secretSettingKeys — "+
				"if it stopped being secret, drop it from both", key)
		}
	}
}

// Автозапуск сверяется по всей строке запуска, а не по факту наличия значения.
// Значение уезжает из-под приложения тремя способами (см. applyAutostart), и
// каждый оставлял стоящую галку над автозапуском, которого нет.
func TestApplyAutostartReconcilesTheRunKey(t *testing.T) {
	if security.AutostartTarget(autostartName) != "" {
		t.Skip("в реестре уже есть запись автозапуска NeoBox — тест её не тронет")
	}
	t.Cleanup(func() { _ = security.RemoveAutostart(autostartName) })

	exePath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	quoted := `"` + exePath + `"`

	applyAutostart(true)
	if got := security.AutostartTarget(autostartName); got != quoted {
		t.Fatalf("после включения в реестре %q, ожидалось %q", got, quoted)
	}

	// Путь, оставшийся от установки в другой каталог: значение есть, но ведёт
	// не туда. Раньше проверка «значение присутствует» считала это исправным.
	stale := filepath.Join(t.TempDir(), "stale.exe")
	if err := os.WriteFile(stale, []byte("MZ"), 0644); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}
	if err := security.SetupAutostart(autostartName, stale); err != nil {
		t.Fatalf("SetupAutostart: %v", err)
	}
	applyAutostart(true)
	if got := security.AutostartTarget(autostartName); got != quoted {
		t.Errorf("устаревший путь не переписан: %q", got)
	}

	applyAutostart(false)
	if got := security.AutostartTarget(autostartName); got != "" {
		t.Errorf("после выключения запись осталась: %q", got)
	}
}

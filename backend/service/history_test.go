package service

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// История переехала из localStorage движка WebView2 в собственный файл ради
// двух вещей: она обязана переживать переустановку и обязана лежать
// зашифрованной, потому что несёт ссылки на прокси. Тесты держат обе.

const testHistoryLink = "vless://99999999-8888-7777-6666-555555555555@history.example.com:443#session"

func historyJSON(t *testing.T, link string) string {
	t.Helper()
	raw, err := json.Marshal([]map[string]interface{}{{
		"id":             "1",
		"server":         "node",
		"protocol":       "vless",
		"address":        "history.example.com",
		"link":           link,
		"connectedAt":    1,
		"disconnectedAt": 2,
		"durationSec":    1,
		"bytesDown":      10,
		"bytesUp":        20,
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func TestHistoryRoundTrips(t *testing.T) {
	s := newSettingsService(t)

	if got := s.GetHistory(); got != "[]" {
		t.Errorf("на свежей установке история не пуста: %q", got)
	}

	want := historyJSON(t, testHistoryLink)
	if !s.SaveHistory(want) {
		t.Fatal("SaveHistory вернул false")
	}

	var saved, loaded []map[string]interface{}
	if err := json.Unmarshal([]byte(want), &saved); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal([]byte(s.GetHistory()), &loaded); err != nil {
		t.Fatalf("GetHistory вернул не массив: %v", err)
	}
	if len(loaded) != len(saved) || loaded[0]["link"] != saved[0]["link"] {
		t.Errorf("история не пережила круг: %v", loaded)
	}
}

// Ради этого перенос и делался в шифрованный файл, а не в settings.json:
// запись истории несёт полную ссылку на прокси.
func TestHistoryIsEncryptedOnDisk(t *testing.T) {
	s := newSettingsService(t)

	if !s.SaveHistory(historyJSON(t, testHistoryLink)) {
		t.Fatal("SaveHistory вернул false")
	}

	raw := readRaw(t, s.historyPath())
	if bytes.Contains(raw, []byte(testHistoryLink)) {
		t.Error("ссылка на прокси лежит в history.json открытым текстом")
	}
}

// «Очистить историю» обязано дойти до диска. Пустой список, который тихо не
// записался бы, оставил бы прежние сессии на месте — и они вернулись бы на
// экран при следующем запуске.
func TestHistoryClearReachesDisk(t *testing.T) {
	s := newSettingsService(t)

	if !s.SaveHistory(historyJSON(t, testHistoryLink)) {
		t.Fatal("SaveHistory вернул false")
	}
	if !s.SaveHistory("[]") {
		t.Fatal("очистка истории вернула false")
	}
	if got := s.GetHistory(); got != "[]" {
		t.Errorf("после очистки история = %q", got)
	}

	raw := readRaw(t, s.historyPath())
	if bytes.Contains(raw, []byte(testHistoryLink)) {
		t.Error("после очистки ссылка всё ещё лежит в файле")
	}
}

func TestSaveHistoryRejectsWhatIsNotAList(t *testing.T) {
	s := newSettingsService(t)

	for _, bad := range []string{`{"id":"1"}`, `"строка"`, `не json вовсе`, ``} {
		if s.SaveHistory(bad) {
			t.Errorf("SaveHistory принял %q", bad)
		}
	}
	if _, err := os.Stat(s.historyPath()); !os.IsNotExist(err) {
		t.Error("отвергнутая запись всё-таки создала файл")
	}
}

// "null" разбирается без ошибки в nil-срез, а nil-срез кодируется обратно в
// "null" — и следующее чтение получило бы не массив.
func TestSaveHistoryNormalisesNull(t *testing.T) {
	s := newSettingsService(t)

	if !s.SaveHistory("null") {
		t.Fatal("SaveHistory отверг null")
	}
	if got := s.GetHistory(); got != "[]" {
		t.Errorf("null сохранился как %q, а не как пустой список", got)
	}
}

func TestSaveHistoryCapsLength(t *testing.T) {
	s := newSettingsService(t)

	entries := make([]map[string]int, maxHistoryEntries+50)
	for i := range entries {
		entries[i] = map[string]int{"id": i}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !s.SaveHistory(string(raw)) {
		t.Fatal("SaveHistory вернул false")
	}

	var loaded []map[string]int
	if err := json.Unmarshal([]byte(s.GetHistory()), &loaded); err != nil {
		t.Fatalf("GetHistory вернул не массив: %v", err)
	}
	if len(loaded) != maxHistoryEntries {
		t.Errorf("сохранено %d записей, ожидалось %d", len(loaded), maxHistoryEntries)
	}
	// Обрезается хвост: список приходит новыми записями вперёд, и терять надо
	// самые старые.
	if loaded[0]["id"] != 0 {
		t.Errorf("обрезано не с того конца: первая запись id=%d", loaded[0]["id"])
	}
}

// Экспорт настроек намеренно не берёт историю: в ней лежат ссылки на прокси, а
// переносимый файл отдаётся другому человеку.
func TestExportedSettingsCarryNoHistory(t *testing.T) {
	s := newSettingsService(t)

	if !s.SaveHistory(historyJSON(t, testHistoryLink)) {
		t.Fatal("SaveHistory вернул false")
	}

	s.fileMu.Lock()
	settings := s.readPlainSettingsLocked()
	s.fileMu.Unlock()

	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(raw, []byte(testHistoryLink)) {
		t.Error("история просочилась в открытую половину настроек")
	}
}

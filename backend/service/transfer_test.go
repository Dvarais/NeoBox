package service

import (
	"encoding/json"
	"strings"
	"testing"

	"NeoBox/backend/i18n"
)

// Разбор переносимого файла — единственное место, где в приложение попадает
// содержимое, пришедшее с чужой машины. Тесты держат обе его обязанности:
// узнавать свой формат и не пропускать учётные данные.

func envelopeJSON(t *testing.T, kind string, schema int, settings map[string]interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(transferEnvelope{
		Kind: kind, Schema: schema, AppVersion: "test", ExportedAt: "now", Settings: settings,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestParseTransferAcceptsOwnFormat(t *testing.T) {
	raw := envelopeJSON(t, transferKind, transferSchema, map[string]interface{}{
		"dns":      "1.1.1.1",
		"bypassRu": true,
	})

	got, err := parseTransfer(raw)
	if err != nil {
		t.Fatalf("свой же файл не принят: %v", err)
	}
	if got["dns"] != "1.1.1.1" || got["bypassRu"] != true {
		t.Errorf("настройки потерялись при разборе: %v", got)
	}
}

func TestParseTransferStripsCredentials(t *testing.T) {
	// Файл правится руками, и в него можно вписать что угодно. Ключи со
	// ссылками обязаны отсекаться: иначе чужие UUID и пароли уехали бы в
	// state.json этой машины — в её собственное зашифрованное хранилище.
	raw := envelopeJSON(t, transferKind, transferSchema, map[string]interface{}{
		"dns":                "1.1.1.1",
		"lastSelectedServer": "vless://secret@evil.example:443",
		"favoriteLinks":      []string{"trojan://password@host:443"},
	})

	got, err := parseTransfer(raw)
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	for _, key := range secretSettingKeys {
		if _, present := got[key]; present {
			t.Errorf("ключ с учётными данными %q прошёл через импорт", key)
		}
	}
	if got["dns"] != "1.1.1.1" {
		t.Error("вместе с секретами вычистились и обычные настройки")
	}
}

func TestParseTransferRejectsForeignFile(t *testing.T) {
	cases := map[string][]byte{
		"чужой JSON":       []byte(`{"hello":"world"}`),
		"не JSON":          []byte(`не файл вовсе`),
		"пустой объект":    []byte(`{}`),
		"нет поля settings": envelopeJSON(t, transferKind, transferSchema, nil),
	}
	for name, raw := range cases {
		if _, err := parseTransfer(raw); err == nil {
			t.Errorf("%s: файл принят, а не должен был", name)
		}
	}
}

func TestParseTransferRejectsNewerSchema(t *testing.T) {
	// Формат новее нашего мог бы означать что угодно, вплоть до других значений
	// у знакомых ключей. Отказ с внятной причиной лучше молчаливого импорта.
	//
	// Язык выставляется явно: i18n держит выбор в глобальной переменной, и без
	// этого тест зависел бы от того, какой тест пакета отработал перед ним.
	t.Cleanup(func() { i18n.SetLanguage("RU") })
	i18n.SetLanguage("RU")

	raw := envelopeJSON(t, transferKind, transferSchema+1, map[string]interface{}{"dns": "1.1.1.1"})

	_, err := parseTransfer(raw)
	if err == nil {
		t.Fatal("файл более новой версии принят")
	}
	if !strings.Contains(err.Error(), "новой версией") {
		t.Errorf("причина отказа непонятна пользователю: %v", err)
	}
	// Номера в тексте — то, ради чего сообщение вообще берёт аргументы: без них
	// пользователь не поймёт, насколько новее файл.
	if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "1") {
		t.Errorf("в тексте нет номеров форматов: %v", err)
	}
}

// Отказ обязан говорить на языке интерфейса. До появления i18n в этом файле
// текст был захардкожен по-русски, и англоязычный пользователь получал русскую
// ошибку в ответ на английскую кнопку.
func TestParseTransferSpeaksTheInterfaceLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage("RU") })

	raw := []byte(`{"hello":"world"}`)

	i18n.SetLanguage("EN")
	_, err := parseTransfer(raw)
	if err == nil {
		t.Fatal("чужой файл принят")
	}
	if !strings.Contains(err.Error(), "not a NeoBox settings file") {
		t.Errorf("при EN ошибка не по-английски: %v", err)
	}

	i18n.SetLanguage("RU")
	_, err = parseTransfer(raw)
	if err == nil {
		t.Fatal("чужой файл принят")
	}
	if !strings.Contains(err.Error(), "не файл настроек NeoBox") {
		t.Errorf("при RU ошибка не по-русски: %v", err)
	}
}

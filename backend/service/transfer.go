package service

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"NeoBox/backend/i18n"
)

// Перенос настроек между машинами.
//
// Задача бытовая: человек настроил у себя маршруты, раздельное туннелирование,
// DNS и галки безопасности, а теперь то же самое нужно другу. Пересобирать это
// руками по второму разу — единственный способ, который был доступен до сих
// пор.
//
// Граница того, что можно отдавать, в коде уже проведена, и переносу остаётся
// ей следовать: settings.json намеренно хранится открытым текстом, потому что
// в нём нет учётных данных, а всё, что их несёт, вынесено в зашифрованные
// state.json (secretSettingKeys) и subscriptions.json. Экспорт берёт первое и
// не трогает второе.
//
// Подписки не экспортируются сознательно, а не по недосмотру: это чужие ссылки
// с UUID и паролями, они привязаны к машине через DPAPI, и раздавать их файлом
// означало бы обойти ровно ту защиту, ради которой их шифруют.
//
// Весь текст отсюда виден человеку — заголовками системных диалогов и причиной
// отказа, — поэтому берётся из backend/i18n, а не пишется здесь по-русски.
// Ошибки собираются errors.New поверх переведённой строки, а не fmt.Errorf с
// %w: за границу Wails они уезжают строками в JavaScript, где от цепочки
// обёрток всё равно ничего не остаётся, а причина уже вписана в текст через %v.

// transferSchema — версия формата переносимого файла. Растёт, только если
// изменится сама обёртка; поля настроек внутри могут появляться и исчезать
// свободно, импорт переносит те, что есть.
const transferSchema = 1

// transferKind отличает файл настроек NeoBox от любого другого JSON, который
// пользователь может выбрать в диалоге по ошибке.
const transferKind = "neobox-settings"

type transferEnvelope struct {
	Kind       string                 `json:"kind"`
	Schema     int                    `json:"schema"`
	AppVersion string                 `json:"appVersion"`
	ExportedAt string                 `json:"exportedAt"`
	Settings   map[string]interface{} `json:"settings"`
}

// ExportSettings сохраняет переносимую часть настроек в файл по выбору
// пользователя и возвращает путь к нему. Пустая строка — пользователь закрыл
// диалог; в этом случае это не ошибка и говорить об этом нечего.
func (s *AppService) ExportSettings() (string, error) {
	// Через s.context(), а не чтением поля: его пишет главный поток, а читают
	// горутины sing-box, и голое чтение здесь было гонкой.
	wCtx := s.context()
	if wCtx == nil {
		return "", errors.New(i18n.T(i18n.ErrWindowNotReady))
	}

	s.fileMu.Lock()
	settings := s.readPlainSettingsLocked()
	s.fileMu.Unlock()

	// Открытая половина не содержит учётных данных по построению, но полагаться
	// на это вслепую нельзя: файл правится руками, а миграция со старых версий
	// могла не завершиться и оставить в нём ключи со ссылками.
	splitSecretSettings(settings)

	envelope := transferEnvelope{
		Kind:       transferKind,
		Schema:     transferSchema,
		AppVersion: currentVersion,
		ExportedAt: time.Now().Format(time.RFC3339),
		Settings:   settings,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", errors.New(i18n.T(i18n.ErrTransferEncode, err))
	}

	path, err := wailsruntime.SaveFileDialog(wCtx, wailsruntime.SaveDialogOptions{
		Title:                i18n.T(i18n.TransferExportTitle),
		DefaultFilename:      "neobox-settings-" + time.Now().Format("2006-01-02") + ".json",
		Filters:              []wailsruntime.FileFilter{{DisplayName: i18n.T(i18n.TransferJSONFilter), Pattern: "*.json"}},
		CanCreateDirectories: true,
	})
	if err != nil {
		return "", errors.New(i18n.T(i18n.ErrTransferSaveDialog, err))
	}
	if path == "" {
		return "", nil // отменено пользователем
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", errors.New(i18n.T(i18n.ErrTransferWrite, err))
	}
	return path, nil
}

// ImportSettings читает файл настроек и применяет его.
//
// Заменяется только открытая половина: выбранный сервер и избранное остаются
// свои, потому что они лежат в state.json и переносимый файл их не содержит.
// Возвращает путь импортированного файла; пустая строка — пользователь закрыл
// диалог.
func (s *AppService) ImportSettings() (string, error) {
	wCtx := s.context()
	if wCtx == nil {
		return "", errors.New(i18n.T(i18n.ErrWindowNotReady))
	}

	path, err := wailsruntime.OpenFileDialog(wCtx, wailsruntime.OpenDialogOptions{
		Title:   i18n.T(i18n.TransferImportTitle),
		Filters: []wailsruntime.FileFilter{{DisplayName: i18n.T(i18n.TransferJSONFilter), Pattern: "*.json"}},
	})
	if err != nil {
		return "", errors.New(i18n.T(i18n.ErrTransferOpenDialog, err))
	}
	if path == "" {
		return "", nil // отменено пользователем
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New(i18n.T(i18n.ErrTransferRead, err))
	}

	incoming, err := parseTransfer(raw)
	if err != nil {
		return "", err
	}

	// Текущие настройки целиком, вместе с зашифрованной половиной: SaveSettings
	// заменяет её тоже, и передать ему объект без избранного значило бы стереть
	// избранное.
	var current map[string]interface{}
	if err := json.Unmarshal([]byte(s.GetSettings()), &current); err != nil || current == nil {
		current = map[string]interface{}{}
	}
	for key, value := range incoming {
		current[key] = value
	}

	// Через SaveSettings, а не записью в файл: там же живут автозапуск в
	// реестре и смена языка трея. Импорт, обошедший этот путь, поменял бы
	// галку «запускать с Windows», не тронув сам реестр.
	if !s.SaveSettings(marshalSettings(current)) {
		return "", errors.New(i18n.T(i18n.ErrTransferSave))
	}
	return path, nil
}

// parseTransfer проверяет обёртку и возвращает переносимые поля настроек,
// очищенные от всего, чему в переносимом файле не место.
func parseTransfer(raw []byte) (map[string]interface{}, error) {
	var envelope transferEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, errors.New(i18n.T(i18n.ErrTransferMalformed, err))
	}
	if envelope.Kind != transferKind {
		return nil, errors.New(i18n.T(i18n.ErrTransferForeign))
	}
	if envelope.Schema > transferSchema {
		return nil, errors.New(i18n.T(i18n.ErrTransferNewerSchema, envelope.Schema, transferSchema))
	}
	if envelope.Settings == nil {
		return nil, errors.New(i18n.T(i18n.ErrTransferNoSettings))
	}

	// Файл мог быть отредактирован руками. Ключи со ссылками на серверы
	// вычищаются здесь, иначе они уехали бы в state.json этой машины — то
	// есть чужие учётные данные попали бы в чужое зашифрованное хранилище.
	settings := envelope.Settings
	splitSecretSettings(settings)
	// Файл мог быть выгружен версией, где ещё жило поле «домены для прямого
	// доступа». Оно сворачивается здесь по той же причине, по которой
	// мигрируется при старте: импорт не должен молча терять маршруты.
	foldCustomDirect(settings)
	return settings, nil
}

// marshalSettings кодирует настройки для SaveSettings, который принимает
// строку. Ошибка здесь невозможна: на вход приходит то, что только что было
// разобрано из JSON.
func marshalSettings(settings map[string]interface{}) string {
	out, err := json.Marshal(settings)
	if err != nil {
		return "{}"
	}
	return string(out)
}

package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"NeoBox/backend/storage"
)

// История сессий.
//
// До этого файла история жила единственной строкой в localStorage движка
// WebView2, и это была ошибка сразу в двух отношениях.
//
// Первое — сохранность. localStorage принадлежит не приложению, а профилю
// WebView2 рядом с ним. Переустановка, чистка профиля или перенос портативной
// сборки на другую машину уносили целый экран из семи вместе с собой, молча и
// без возможности что-то восстановить: экспорт настроек про него не знал,
// потому что экспорт читает settings.json, а история туда никогда не попадала.
//
// Второе — секреты. Запись истории несёт поле link — полную ссылку на прокси, с
// UUID и паролем, ровно того вида, ради которого subscriptions.json и state.json
// шифруются. Держать её в localStorage означало хранить в открытом виде рядом с
// приложением то самое, что двумя файлами дальше запечатано DPAPI. Поэтому
// history.json шифруется так же, как подписки, и по той же причине не
// экспортируется: ExportSettings отдаёт только то, в чём учётных данных нет.

// maxHistoryEntries ограничивает файл сверху. Фронтенд обрезает список сам, но
// предел повторён здесь: history.json пишется без участия человека и растёт
// строго от сессий, поэтому единственное, что может его раздуть, — ошибка в
// вызывающем коде, и ловить её лучше до записи на диск.
const maxHistoryEntries = 100

// historyPath возвращает путь к зашифрованному файлу истории.
func (s *AppService) historyPath() string {
	return filepath.Join(s.userDataDir, "history.json")
}

// GetHistory отдаёт историю сессий строкой JSON. Отсутствующий файл — обычное
// состояние на свежей установке, а не ошибка: возвращается пустой массив.
func (s *AppService) GetHistory() string {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	data, found, err := storage.ReadSecret(s.historyPath())
	if err != nil {
		fmt.Printf("[history] read error: %v\n", err)
		return "[]"
	}
	if !found {
		return "[]"
	}
	// Разбор, а не json.Valid: файл, который разбирается, но содержит объект или
	// строку вместо массива, прошёл бы проверку и сломал бы вкладку «История».
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		fmt.Println("[history] file contains invalid JSON — treating as empty")
		return "[]"
	}
	return string(data)
}

// SaveHistory шифрует и записывает историю. Возвращает false, если записать не
// удалось, чтобы фронтенд не считал сохранённым то, чего на диске нет.
//
// Пустой список — законное значение: так выглядит нажатие «Очистить историю».
// storage.WriteSecret отказывается писать пустое содержимое, поэтому пустой
// массив уходит на диск как "[]" — два байта, но это именно запись, а не
// пропуск, иначе очистка оставила бы прежний файл на месте.
func (s *AppService) SaveHistory(historyJSON string) bool {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(historyJSON), &entries); err != nil {
		fmt.Println("[history] refusing to write invalid JSON")
		return false
	}
	if len(entries) > maxHistoryEntries {
		entries = entries[:maxHistoryEntries]
	}
	// Вход "null" разбирается без ошибки в nil-срез, а nil-срез кодируется
	// обратно в "null" — и следующее чтение получило бы не массив. Пустой
	// список обязан остаться пустым списком.
	if entries == nil {
		entries = []json.RawMessage{}
	}

	data, err := json.Marshal(entries)
	if err != nil {
		fmt.Printf("[history] encode error: %v\n", err)
		return false
	}

	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if err := storage.WriteSecret(s.historyPath(), data); err != nil {
		fmt.Printf("[history] write error: %v\n", err)
		return false
	}
	return true
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/i18n"
	"NeoBox/backend/storage"
)

// Subscriptions: encrypted storage, fetching and the auto-update scheduler.

type Subscription struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Links   []string `json:"links"`
	Loading bool     `json:"loading"`

	// Состояние автообновления. Все три поля появились вместе и решают одну
	// задачу: до них обновление было полностью немым. Обновление молча
	// пропускало подписку, которую не удалось загрузить (`if err == nil`),
	// не оставляя следа ни в файле, ни на экране, — и провалившееся обновление
	// выглядело как «список серверов почему-то старый». Продукт обещает
	// обратное: когда он чего-то не сделал, он говорит, чего именно и почему.

	// UpdatedAt — время последней УСПЕШНОЙ загрузки, unix-миллисекунды. Ноль
	// означает «ни разу не обновлялась с тех пор, как появились эти поля»;
	// именно ноль, а не отсутствие поля, потому что подписки, заведённые
	// прежними сборками, приходят без него.
	UpdatedAt int64 `json:"updatedAt,omitempty"`
	// LastError — причина последней неудачи, уже переведённая. Очищается при
	// первой же успешной загрузке: держать устаревшую ошибку рядом со свежим
	// списком серверов хуже, чем не держать никакой.
	LastError string `json:"lastError,omitempty"`
	// IntervalHours — свой период обновления. Ноль означает «как у всех»
	// (defaultUpdateIntervalHours). Отдельное поле, а не общая настройка,
	// потому что провайдеры ротируют узлы с разной частотой, и единственный
	// период на всех — это либо лишние запросы к одним, либо устаревшие узлы у
	// других.
	IntervalHours int `json:"intervalHours,omitempty"`
}

// Периоды автообновления.
const (
	// defaultUpdateIntervalHours — прежнее и по-прежнему разумное значение по
	// умолчанию.
	defaultUpdateIntervalHours = 24
	// minUpdateIntervalHours не даёт превратить обновление в опрос: подписка —
	// это запрос к чужому серверу, и час — уже часто.
	minUpdateIntervalHours = 1
	// maxUpdateIntervalHours — месяц. Больше означает «не обновлять», и для
	// этого есть галка автообновления.
	maxUpdateIntervalHours = 720

	// schedulerTick — как часто планировщик проверяет, не подошёл ли срок хоть
	// у одной подписки. Не равен периоду обновления: периоды теперь у каждой
	// подписки свои, и единственный способ соблюсти их все — просыпаться чаще
	// самого короткого из них и смотреть на часы.
	schedulerTick = 10 * time.Minute
)

// updateInterval возвращает период подписки в виде длительности, приводя
// значение к допустимым границам. Файл правится руками только через интерфейс,
// но границы всё равно проверяются здесь: это единственное место, где период
// превращается во время следующего запроса.
func (sub Subscription) updateInterval() time.Duration {
	hours := sub.IntervalHours
	if hours <= 0 {
		hours = defaultUpdateIntervalHours
	}
	if hours < minUpdateIntervalHours {
		hours = minUpdateIntervalHours
	}
	if hours > maxUpdateIntervalHours {
		hours = maxUpdateIntervalHours
	}
	return time.Duration(hours) * time.Hour
}

// dueForUpdate сообщает, подошёл ли срок обновления к моменту now.
//
// Подписка, которая не обновлялась ни разу (UpdatedAt == 0), считается
// просроченной: так ведёт себя и свежедобавленная, и пришедшая из сборки, где
// отметок времени ещё не было.
func (sub Subscription) dueForUpdate(now time.Time) bool {
	if sub.URL == "" {
		return false // из буфера обмена, обновлять неоткуда
	}
	if sub.UpdatedAt == 0 {
		return true
	}
	return now.Sub(time.UnixMilli(sub.UpdatedAt)) >= sub.updateInterval()
}

// subscriptionsPath returns the location of the encrypted subscriptions file.
func (s *AppService) subscriptionsPath() string {
	return filepath.Join(s.userDataDir, "subscriptions.json")
}

// readSubscriptionsLocked returns the decrypted subscriptions JSON, or nil when
// the file is absent, unreadable, or does not contain valid JSON.
// Callers must already hold fileMu.
func (s *AppService) readSubscriptionsLocked() []byte {
	data, found, err := storage.ReadSecret(s.subscriptionsPath())
	if err != nil {
		fmt.Printf("[subscriptions] read error: %v\n", err)
		return nil
	}
	if !found {
		return nil
	}
	if !json.Valid(data) {
		fmt.Println("[subscriptions] file contains invalid JSON — treating as empty")
		return nil
	}
	return data
}

// GetSubscriptions returns the stored subscriptions as a JSON string.
//
// The file itself is encrypted at rest (AES-256-GCM under a DPAPI-protected
// key) because it holds full proxy links — UUIDs and passwords — so it is NOT
// user-editable, unlike settings.json.
// NOTE: GetSubscriptions is a pure read — it never writes to disk.
func (s *AppService) GetSubscriptions() string {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	data := s.readSubscriptionsLocked()
	if data == nil {
		return "[]"
	}
	rawJSON := string(data)

	// Filter out the NeoBox Free bootstrap subscription in-memory only.
	// NOTE: we do NOT write back to disk here — see purgeBootstrapSub().
	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err == nil {
		hasBootstrap := false
		cleanedSubs := subs[:0]
		for _, sub := range subs {
			if sub.ID == "bootstrap-free-subs" {
				hasBootstrap = true
			} else {
				cleanedSubs = append(cleanedSubs, sub)
			}
		}
		if hasBootstrap {
			if merged, err := json.Marshal(cleanedSubs); err == nil {
				rawJSON = string(merged)
			}
		}
	}

	return rawJSON
}

// SaveSubscriptions encrypts and saves the subscription list.
// It also purges the bootstrap subscription from disk if present.
func (s *AppService) SaveSubscriptions(subsJSON string) bool {
	// Purge bootstrap sub from the JSON being saved.
	subsJSON = purgeBootstrapSub(subsJSON)

	// Validate JSON before writing
	if !json.Valid([]byte(subsJSON)) {
		fmt.Println("[SaveSubscriptions] refusing to write invalid JSON")
		return false
	}

	// Indent the payload before sealing it. The file on disk is ciphertext, but
	// the plaintext stays readable for anyone debugging via an export.
	var pretty []interface{}
	var out []byte
	if err := json.Unmarshal([]byte(subsJSON), &pretty); err == nil {
		out, _ = json.MarshalIndent(pretty, "", "  ")
	}
	if out == nil {
		out = []byte(subsJSON)
	}

	s.fileMu.Lock()
	err := storage.WriteSecret(s.subscriptionsPath(), out)
	s.fileMu.Unlock()
	if err != nil {
		fmt.Printf("[SaveSubscriptions] write failed: %v\n", err)
		return false
	}

	// Rebuild AFTER releasing fileMu. RebuildTrayServers takes fileMu itself to
	// read the list back and then trayMu to apply it; doing that while still
	// holding fileMu would nest the two locks and invert the ordering used by
	// every other tray path.
	s.RebuildTrayServers()
	return true
}

// purgeBootstrapSub removes the "bootstrap-free-subs" entry from a JSON subscription list.
func purgeBootstrapSub(subsJSON string) string {
	var subs []Subscription
	if err := json.Unmarshal([]byte(subsJSON), &subs); err != nil {
		return subsJSON
	}
	cleaned := subs[:0]
	for _, sub := range subs {
		if sub.ID != "bootstrap-free-subs" {
			cleaned = append(cleaned, sub)
		}
	}
	if len(cleaned) == len(subs) {
		return subsJSON // nothing removed
	}
	if merged, err := json.Marshal(cleaned); err == nil {
		return string(merged)
	}
	return subsJSON
}

// FetchSubscription loads subscription contents from subscription URL.
//
// The error is returned rather than swallowed: Wails rejects the JavaScript
// promise with its text, which is how the user finds out that a subscription
// came back as a login page or in a format NeoBox cannot read. Silently
// returning an empty list left the interface with nothing to say.
func (s *AppService) FetchSubscription(url string) ([]string, error) {
	return core.FetchSubscription(url)
}

// ImportClipboard filters proxy links from raw clipboard string.
func (s *AppService) ImportClipboard(text string) []string {
	var links []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if core.IsProxyLink(trimmed) {
			links = append(links, trimmed)
		}
	}
	return links
}

// StartAutoUpdateScheduler runs a background loop that refreshes each
// subscription on its own schedule, provided the auto-update setting is on.
//
// Раньше цикл спал ровно сутки и обновлял всё разом. Своих периодов у подписок
// не было, а «сутки» отсчитывались от запуска приложения, а не от последней
// загрузки, — так что приложение, которое перезапускают чаще раза в день,
// обновляло подписки при каждом запуске, а то, что не выключают неделями, — раз
// в сутки независимо от того, что просило само обновление.
//
// Теперь цикл просыпается каждые schedulerTick и обновляет только просроченные,
// сверяясь с UpdatedAt каждой подписки. Это же чинит и перезапуски: отметка
// времени лежит на диске и переживает их.
//
// The loop is context-driven so shutdown can stop it: the cancel function lives
// in s.cancelAutoUpdate and is invoked by StopAutoUpdateScheduler.
func (s *AppService) StartAutoUpdateScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stateMu.Lock()
	if s.cancelAutoUpdate != nil {
		s.cancelAutoUpdate() // stop any previous scheduler
	}
	s.cancelAutoUpdate = cancel
	s.stateMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "[subscriptions] recovered panic in StartAutoUpdateScheduler: %v\n", r)
			}
		}()

		// Wait 5 seconds after startup to let the app initialize
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}

		for {
			// Настройка читается на каждом обходе, а не один раз: галку
			// автообновления могут снять, пока цикл спит.
			var settings map[string]interface{}
			_ = json.Unmarshal([]byte(s.GetSettings()), &settings)

			if autoUpdate, _ := settings["autoUpdateSubs"].(bool); autoUpdate {
				s.updateDueSubscriptions(ctx)
			}

			select {
			case <-time.After(schedulerTick):
			case <-ctx.Done():
				return
			}
		}
	}()
}

// updateDueSubscriptions обновляет те подписки, у которых подошёл срок.
func (s *AppService) updateDueSubscriptions(ctx context.Context) {
	s.refreshSubscriptions(ctx, func(sub Subscription) bool {
		return sub.dueForUpdate(time.Now())
	})
}

// StopAutoUpdateScheduler stops the background auto-update goroutine.
// Should be called during application shutdown.
func (s *AppService) StopAutoUpdateScheduler() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.cancelAutoUpdate != nil {
		s.cancelAutoUpdate()
		s.cancelAutoUpdate = nil
	}
}

// UpdateSubscriptionNow загружает одну подписку, не дожидаясь её срока.
//
// Отдельный метод, а не флаг «обновить всё»: «обновить эту» —
// действие человека над конкретной подпиской, и оно не должно ходить в сеть за
// остальными.
func (s *AppService) UpdateSubscriptionNow(id string) {
	s.refreshSubscriptions(context.Background(), func(sub Subscription) bool {
		return sub.ID == id
	})
}

// refreshSubscriptions загружает подписки, отобранные want, и записывает
// результат — в том числе неуспешный.
//
// Запись происходит всегда, когда хоть что-то изменилось, а «изменилось»
// включает и появившуюся ошибку. Прежняя версия сохраняла файл только при
// успехе хотя бы одной загрузки, и провал не оставлял следа нигде: ни отметки
// времени, ни причины. Пользователь видел старый список серверов и никакого
// объяснения — ровно то, что принцип «падать в закрытое состояние и говорить об
// этом» запрещает.
//
// Сначала загружаются все, потом результат накладывается на список — см.
// mergeFetchedSubscriptions. Загрузка идёт без файлового замка, потому что
// длится столько же, сколько чужие серверы отвечают.
func (s *AppService) refreshSubscriptions(ctx context.Context, want func(Subscription) bool) {
	var subs []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &subs); err != nil {
		return
	}

	results := map[string]subFetch{}
	for _, sub := range subs {
		select {
		case <-ctx.Done():
			return // приложение закрывается — недописанное состояние не сохраняем
		default:
		}

		if sub.URL == "" || !want(sub) {
			continue
		}

		links, err := core.FetchSubscription(sub.URL)
		switch {
		case err != nil:
			results[sub.ID] = subFetch{Err: err.Error()}
		case len(links) == 0:
			// Пустой ответ без ошибки: формально загрузка удалась, но серверов
			// в ней нет. Для пользователя это неудача, и назвать её надо так же.
			results[sub.ID] = subFetch{Err: i18n.T(i18n.ErrSubNoNodes)}
		default:
			results[sub.ID] = subFetch{Links: links}
		}
	}

	if len(results) == 0 || !s.mergeFetchedSubscriptions(results) {
		return
	}
	s.emitSafe("subscriptions-updated", nil)
}

// subFetch — исход одной загрузки. Links непуст только при успехе; Err несёт
// уже переведённую причину неудачи.
type subFetch struct {
	Links []string
	Err   string
}

// mergeFetchedSubscriptions накладывает результаты на список, лежащий на диске
// СЕЙЧАС, а не на тот, что был прочитан перед загрузкой.
//
// Разница не теоретическая. Между чтением и записью проходит столько, сколько
// занимают запросы к чужим серверам, — секунды, а на недоступной подписке
// десятки. Всё это время человек может добавить, переименовать или удалить
// подписку в окне, и прежняя версия писала поверх его правки свой устаревший
// снимок: удалённая подписка возвращалась, добавленная исчезала. Сопоставление
// идёт по ID, и подписка, которой на диске уже нет, просто пропускается.
func (s *AppService) mergeFetchedSubscriptions(results map[string]subFetch) bool {
	s.fileMu.Lock()

	var subs []Subscription
	if data := s.readSubscriptionsLocked(); data != nil {
		if err := json.Unmarshal(data, &subs); err != nil {
			s.fileMu.Unlock()
			return false
		}
	}

	changed := false
	for i := range subs {
		result, ok := results[subs[i].ID]
		if !ok {
			continue
		}
		if result.Err != "" {
			// Список серверов при неудаче не трогается. Подписка, которая
			// сегодня отвечает страницей оплаты, — это не подписка без
			// серверов: вчерашние узлы, скорее всего, ещё работают, и стирать
			// их значило бы оставить человека без связи из-за чужого сбоя.
			subs[i].LastError = result.Err
		} else {
			subs[i].Links = result.Links
			subs[i].LastError = ""
			subs[i].UpdatedAt = time.Now().UnixMilli()
		}
		changed = true
	}
	if !changed {
		s.fileMu.Unlock()
		return false
	}

	out, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		s.fileMu.Unlock()
		return false
	}
	err = storage.WriteSecret(s.subscriptionsPath(), out)
	s.fileMu.Unlock()
	if err != nil {
		fmt.Printf("[subscriptions] write failed: %v\n", err)
		return false
	}

	// Вне fileMu: RebuildTrayServers берёт его сам, чтобы прочитать список
	// обратно, и вложение здесь перевернуло бы порядок захвата.
	s.RebuildTrayServers()
	return true
}

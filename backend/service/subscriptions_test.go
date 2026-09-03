package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/storage"
)

// Расписание обновления подписок и запись его исхода.
//
// До этих полей обновление было немым: провалившуюся загрузку код пропускал
// молча, а срок отсчитывался от запуска приложения, а не от последней удачной
// загрузки. Тесты держат обе половины исправления.

func newSubService(t *testing.T) *AppService {
	t.Helper()
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init: %v", err)
	}
	return &AppService{userDataDir: dir, coreManager: core.NewCoreManager()}
}

func TestUpdateIntervalFallsBackAndClamps(t *testing.T) {
	cases := map[string]struct {
		hours int
		want  time.Duration
	}{
		"ноль — значение по умолчанию":      {0, defaultUpdateIntervalHours * time.Hour},
		"отрицательное — тоже по умолчанию": {-5, defaultUpdateIntervalHours * time.Hour},
		"своё значение":                     {6, 6 * time.Hour},
		"слишком большое подрезается":       {10000, maxUpdateIntervalHours * time.Hour},
		"нижняя граница":                    {minUpdateIntervalHours, minUpdateIntervalHours * time.Hour},
	}
	for name, c := range cases {
		if got := (Subscription{IntervalHours: c.hours}).updateInterval(); got != c.want {
			t.Errorf("%s: интервал %v, ожидался %v", name, got, c.want)
		}
	}
}

func TestDueForUpdate(t *testing.T) {
	now := time.Now()

	// Никогда не обновлявшаяся просрочена всегда — включая подписки из сборок,
	// где отметки времени ещё не было.
	if !(Subscription{URL: "https://example.com"}).dueForUpdate(now) {
		t.Error("подписка без отметки времени не считается просроченной")
	}

	// Из буфера обмена обновлять неоткуда, сколько бы времени ни прошло.
	if (Subscription{URL: "", UpdatedAt: 1}).dueForUpdate(now) {
		t.Error("подписка без URL попала в очередь обновления")
	}

	fresh := Subscription{
		URL:           "https://example.com",
		IntervalHours: 6,
		UpdatedAt:     now.Add(-1 * time.Hour).UnixMilli(),
	}
	if fresh.dueForUpdate(now) {
		t.Error("час назад при периоде 6 часов — ещё не срок")
	}

	stale := fresh
	stale.UpdatedAt = now.Add(-7 * time.Hour).UnixMilli()
	if !stale.dueForUpdate(now) {
		t.Error("семь часов назад при периоде 6 часов — уже срок")
	}

	// Период по умолчанию применяется и когда своего нет.
	byDefault := Subscription{
		URL:       "https://example.com",
		UpdatedAt: now.Add(-(defaultUpdateIntervalHours + 1) * time.Hour).UnixMilli(),
	}
	if !byDefault.dueForUpdate(now) {
		t.Error("подписка без своего периода не обновляется по умолчанию")
	}
}

// Главное изменение: неудача обязана оставить след на диске. Раньше её не
// сохраняли вовсе, и провал выглядел как «список серверов почему-то старый».
func TestRefreshRecordsFailureAndKeepsLinks(t *testing.T) {
	s := newSubService(t)

	existing := []string{"vless://a@1.1.1.1:443#a"}
	subs := []Subscription{{
		ID: "s1", Name: "test",
		// Порт, на котором заведомо никто не слушает: загрузка провалится.
		URL:   "http://127.0.0.1:1/subscription",
		Links: existing,
	}}
	raw, err := json.Marshal(subs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !s.SaveSubscriptions(string(raw)) {
		t.Fatal("SaveSubscriptions вернул false")
	}

	s.refreshSubscriptions(context.Background(), func(Subscription) bool { return true })

	var got []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &got); err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("подписок стало %d", len(got))
	}
	if got[0].LastError == "" {
		t.Error("причина неудачи не записана — обновление снова немое")
	}
	if got[0].UpdatedAt != 0 {
		t.Error("неудачная загрузка проставила отметку успешного обновления")
	}
	// Список серверов при неудаче не трогается: вчерашние узлы, скорее всего,
	// ещё работают, и стирать их из-за чужого сбоя нельзя.
	if len(got[0].Links) != 1 || got[0].Links[0] != existing[0] {
		t.Errorf("прежние серверы потеряны при неудачном обновлении: %v", got[0].Links)
	}
}

// Отменённый контекст обязан остановить обход, не записав половину состояния.
func TestRefreshStopsOnCancelledContext(t *testing.T) {
	s := newSubService(t)

	subs := []Subscription{{ID: "s1", Name: "test", URL: "http://127.0.0.1:1/sub"}}
	raw, _ := json.Marshal(subs)
	if !s.SaveSubscriptions(string(raw)) {
		t.Fatal("SaveSubscriptions вернул false")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.refreshSubscriptions(ctx, func(Subscription) bool { return true })

	var got []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &got); err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if got[0].LastError != "" {
		t.Error("отменённый обход всё-таки сходил в сеть и записал результат")
	}
}

// Подписка без URL не должна даже попадать в обход: обновлять её неоткуда.
func TestRefreshSkipsClipboardSubscriptions(t *testing.T) {
	s := newSubService(t)

	subs := []Subscription{{ID: "clip", Name: "clipboard", URL: "", Links: []string{"vless://a@1.1.1.1:443#a"}}}
	raw, _ := json.Marshal(subs)
	if !s.SaveSubscriptions(string(raw)) {
		t.Fatal("SaveSubscriptions вернул false")
	}

	s.refreshSubscriptions(context.Background(), func(Subscription) bool { return true })

	var got []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &got); err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if got[0].LastError != "" {
		t.Errorf("подписке из буфера приписали ошибку загрузки: %q", got[0].LastError)
	}
}

// Поля расписания обязаны переживать круг через хранилище: на них опирается
// решение, обновлять ли подписку после перезапуска приложения.
func TestSchedulingFieldsSurviveStorage(t *testing.T) {
	s := newSubService(t)

	when := time.Now().UnixMilli()
	subs := []Subscription{{
		ID: "s1", Name: "test", URL: "https://example.com",
		UpdatedAt: when, IntervalHours: 6, LastError: "боль",
	}}
	raw, _ := json.Marshal(subs)
	if !s.SaveSubscriptions(string(raw)) {
		t.Fatal("SaveSubscriptions вернул false")
	}

	var got []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &got); err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if got[0].UpdatedAt != when || got[0].IntervalHours != 6 || got[0].LastError != "боль" {
		t.Errorf("поля расписания не пережили круг: %+v", got[0])
	}
}

// Подписки шифруются целиком, и новые поля ничего в этом не меняют — но
// проверить стоит: они добавились в ту же структуру.
func TestSubscriptionLinksStayEncrypted(t *testing.T) {
	s := newSubService(t)

	const secret = "vless://11111111-2222-3333-4444-555555555555@node.example.com:443#s"
	subs := []Subscription{{ID: "s1", Name: "test", URL: "https://example.com", Links: []string{secret}}}
	raw, _ := json.Marshal(subs)
	if !s.SaveSubscriptions(string(raw)) {
		t.Fatal("SaveSubscriptions вернул false")
	}

	if onDisk := readRaw(t, s.subscriptionsPath()); strings.Contains(string(onDisk), secret) {
		t.Error("ссылка лежит в subscriptions.json открытым текстом")
	}
}

// Между чтением списка и записью результата проходит вся сетевая работа, и за
// это время человек успевает править подписки в окне. Прежняя версия писала
// поверх его правки свой устаревший снимок: удалённая подписка возвращалась,
// добавленная исчезала. Наложение идёт по ID на то, что лежит на диске сейчас.
func TestMergeFetchedSubscriptionsRespectsConcurrentEdits(t *testing.T) {
	s := newSubService(t)

	// Состояние диска на момент, когда загрузка уже закончилась: "gone" удалили,
	// "fresh" добавили, а "keep" переименовали.
	onDisk := []Subscription{
		{ID: "keep", Name: "Переименована", URL: "https://example.com/keep", Links: []string{"old://node"}},
		{ID: "fresh", Name: "Добавлена во время загрузки", URL: "https://example.com/fresh"},
	}
	payload, _ := json.Marshal(onDisk)
	if !s.SaveSubscriptions(string(payload)) {
		t.Fatal("SaveSubscriptions вернул false")
	}

	// Результаты загрузки, начатой до правки: они знают про "keep" и про уже
	// удалённую "gone", и ничего не знают про "fresh".
	ok := s.mergeFetchedSubscriptions(map[string]subFetch{
		"keep": {Links: []string{"new://node"}},
		"gone": {Links: []string{"resurrected://node"}},
	})
	if !ok {
		t.Fatal("mergeFetchedSubscriptions вернул false")
	}

	var got []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &got); err != nil {
		t.Fatalf("разбор сохранённого списка: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("на диске %d подписок, ожидалось 2: %+v", len(got), got)
	}
	if got[0].ID != "keep" || got[1].ID != "fresh" {
		t.Fatalf("состав списка изменился: %+v", got)
	}
	if got[0].Name != "Переименована" {
		t.Errorf("переименование затёрто: %q", got[0].Name)
	}
	if len(got[0].Links) != 1 || got[0].Links[0] != "new://node" {
		t.Errorf("узлы не обновились: %+v", got[0].Links)
	}
	if got[0].UpdatedAt == 0 {
		t.Error("отметка времени не проставлена")
	}
}

// Неудачная загрузка оставляет узлы на месте и записывает причину: подписка,
// которая сегодня отвечает страницей оплаты, — это не подписка без серверов.
func TestMergeFetchedSubscriptionsKeepsLinksOnFailure(t *testing.T) {
	s := newSubService(t)
	payload, _ := json.Marshal([]Subscription{
		{ID: "one", URL: "https://example.com", Links: []string{"vless://node"}, UpdatedAt: 42},
	})
	if !s.SaveSubscriptions(string(payload)) {
		t.Fatal("SaveSubscriptions вернул false")
	}

	if !s.mergeFetchedSubscriptions(map[string]subFetch{"one": {Err: "402 Payment Required"}}) {
		t.Fatal("mergeFetchedSubscriptions вернул false")
	}

	var got []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &got); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if len(got[0].Links) != 1 {
		t.Errorf("узлы стёрты при неудачной загрузке: %+v", got[0].Links)
	}
	if got[0].LastError != "402 Payment Required" {
		t.Errorf("причина неудачи не записана: %q", got[0].LastError)
	}
	if got[0].UpdatedAt != 42 {
		t.Errorf("отметка времени сдвинулась при неудаче: %d", got[0].UpdatedAt)
	}
}

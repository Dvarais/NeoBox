package service

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/storage"
)

// Переключение сервера при живой сети и мёртвом узле.
//
// До этого watchdog при молчащем сервере уходил в ожидание с нарастающей
// паузой и не пробовал ничего другого: при заблокированном узле и работающей
// сети он ждал бы его бесконечно. Тесты держат обе стороны нового поведения —
// что кандидат находится, и что при мёртвой сети переключаться некуда.

func serviceWithSubs(t *testing.T, links ...string) *AppService {
	t.Helper()
	dir := t.TempDir()
	// subscriptions.json шифруется на диске, поэтому слой хранения надо поднять
	// так же, как это делает запуск приложения.
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init: %v", err)
	}
	s := &AppService{userDataDir: dir, coreManager: core.NewCoreManager()}
	subs := []Subscription{{ID: "s1", Name: "test", Links: links}}
	raw, err := json.Marshal(subs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !s.SaveSubscriptions(string(raw)) {
		t.Fatal("не удалось сохранить подписки")
	}
	return s
}

// listener поднимает локальный TCP-порт и возвращает vless-ссылку на него:
// pickLiveServer проверяет живость обычным подключением, поэтому настоящий
// прокси-сервер для теста не нужен.
func listener(t *testing.T) (link string, close func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return "vless://00000000-0000-0000-0000-000000000000@127.0.0.1:" + port + "?type=tcp#live",
		func() { _ = ln.Close() }
}

// deadLink указывает на закрытый порт: адрес разбирается, но не отвечает.
const deadLink = "vless://00000000-0000-0000-0000-000000000000@127.0.0.1:1?type=tcp#dead"

func TestPickLiveServerFindsAnsweringNode(t *testing.T) {
	live, closeLn := listener(t)
	defer closeLn()

	got := pickLiveServer(context.Background(), []string{deadLink, live})
	if got != live {
		t.Fatalf("ожидалась живая ссылка, получено %q", got)
	}
}

func TestPickLiveServerReturnsNothingWhenAllDead(t *testing.T) {
	// Никто не ответил — значит дело в сети, а не в сервере, и переключаться
	// некуда. Именно этим watchdog отличает одно от другого.
	start := time.Now()
	if got := pickLiveServer(context.Background(), []string{deadLink, deadLink}); got != "" {
		t.Fatalf("мёртвые узлы не должны выбираться, получено %q", got)
	}
	if elapsed := time.Since(start); elapsed > watchdogCandidateTimeout+2*time.Second {
		t.Errorf("опрос занял %v — кандидаты опрашиваются последовательно, а не параллельно", elapsed)
	}
}

func TestPickLiveServerEmptyCandidates(t *testing.T) {
	if got := pickLiveServer(context.Background(), nil); got != "" {
		t.Fatalf("пустой список кандидатов дал %q", got)
	}
}

func TestWatchdogCandidatesExcludesTried(t *testing.T) {
	s := serviceWithSubs(t, "vless://a@1.1.1.1:443#a", "vless://b@2.2.2.2:443#b", "vless://c@3.3.3.3:443#c")

	all := s.watchdogCandidates(map[string]bool{})
	if len(all) != 3 {
		t.Fatalf("ожидалось 3 кандидата, получено %d: %v", len(all), all)
	}

	rest := s.watchdogCandidates(map[string]bool{"vless://b@2.2.2.2:443#b": true})
	if len(rest) != 2 {
		t.Fatalf("исключённый сервер не убран: %v", rest)
	}
	for _, link := range rest {
		if strings.Contains(link, "2.2.2.2") {
			t.Errorf("уже испробованный сервер вернулся в кандидаты: %s", link)
		}
	}
}

func TestWatchdogCandidatesCapped(t *testing.T) {
	// Подписки бывают на сотни узлов. Опрашивать их все — всплеск исходящих
	// соединений ровно в момент аварии.
	links := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		links = append(links, "vless://x@10.0.0.1:443#n"+string(rune('a'+i%26)))
	}
	s := serviceWithSubs(t, links...)

	if got := len(s.watchdogCandidates(map[string]bool{})); got > watchdogMaxCandidates {
		t.Fatalf("кандидатов %d, потолок %d", got, watchdogMaxCandidates)
	}
}

// switchToLiveServer зовут из двух веток: когда узел молчит и когда он
// отвечает, но трафик через него не идёт. Обе обязаны запомнить текущий сервер
// как испробованный — иначе переключение вернулось бы на него же.
func TestSwitchToLiveServerExcludesCurrent(t *testing.T) {
	live, closeLn := listener(t)
	defer closeLn()

	current := deadLink
	s := serviceWithSubs(t, current, live)
	s.watchdogLink = current

	tried := map[string]bool{}
	got := s.switchToLiveServer(context.Background(), tried)
	if got != live {
		t.Fatalf("выбран %q, ожидался живой узел %q", got, live)
	}
	if !tried[current] {
		t.Error("текущий сервер не помечен как испробованный")
	}
	if s.watchdogLink != live {
		t.Errorf("watchdogLink остался %q", s.watchdogLink)
	}
}

// Менять не на что — метод обязан сказать об этом, а не подсунуть тот же узел.
func TestSwitchToLiveServerWithoutAlternatives(t *testing.T) {
	s := serviceWithSubs(t, deadLink)
	s.watchdogLink = deadLink

	tried := map[string]bool{}
	if got := s.switchToLiveServer(context.Background(), tried); got != "" {
		t.Fatalf("при мёртвых кандидатах выбрано %q", got)
	}
	if s.watchdogLink != deadLink {
		t.Errorf("ссылка подменена, хотя выбирать было не из чего: %q", s.watchdogLink)
	}
}

// Второй заход не должен предлагать то, что уже отвергнуто в первом: иначе
// переключение ходило бы по кругу между двумя мёртвыми узлами.
func TestSwitchToLiveServerAccumulatesTried(t *testing.T) {
	first := "vless://a@127.0.0.1:1?type=tcp#first"
	second := "vless://b@127.0.0.1:2?type=tcp#second"
	s := serviceWithSubs(t, first, second)
	s.watchdogLink = first

	tried := map[string]bool{}
	s.switchToLiveServer(context.Background(), tried)
	s.watchdogLink = second
	s.switchToLiveServer(context.Background(), tried)

	if !tried[first] || !tried[second] {
		t.Errorf("список испробованных не накапливается: %v", tried)
	}
	if got := s.watchdogCandidates(tried); len(got) != 0 {
		t.Errorf("после двух неудач остались кандидаты: %v", got)
	}
}

func TestWatchdogCandidatesNoSubscriptions(t *testing.T) {
	dir := t.TempDir()
	if err := storage.Init(dir); err != nil {
		t.Fatalf("storage.Init: %v", err)
	}
	s := &AppService{userDataDir: dir, coreManager: core.NewCoreManager()}
	if got := s.watchdogCandidates(map[string]bool{}); len(got) != 0 {
		t.Fatalf("без подписок кандидатов быть не должно, получено %v", got)
	}
}

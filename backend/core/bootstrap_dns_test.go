package core

import (
	"context"
	"net"
	"testing"
	"time"
)

// Ответ DoH — не список адресов, а список записей вперемешку. Разбор, который
// берёт из Answer всё подряд, на первом же имени с CNAME подставит в конфиг
// доменное имя вместо адреса.
func TestParseDoHAnswerTakesOnlyAddresses(t *testing.T) {
	body := []byte(`{"Status":0,"Answer":[
		{"name":"vpn.example.","type":5,"data":"edge.example."},
		{"name":"edge.example.","type":1,"data":"203.0.113.7"},
		{"name":"edge.example.","type":28,"data":"2001:db8::1"}
	]}`)
	got := parseDoHAnswer(body)
	if len(got) != 2 {
		t.Fatalf("got %v, want the A and the AAAA", got)
	}
	if !got[0].Equal(net.ParseIP("203.0.113.7")) || !got[1].Equal(net.ParseIP("2001:db8::1")) {
		t.Errorf("got %v", got)
	}
}

// NXDOMAIN и подобное приходят с кодом 200 и телом JSON. Ответ со Status != 0
// адресов не несёт, и принимать его за успех нельзя: вызывающий тогда не
// попробует ни второй резолвер, ни системный.
func TestParseDoHAnswerRejectsFailures(t *testing.T) {
	for _, body := range []string{
		`{"Status":3}`,
		`{"Status":0,"Answer":[{"type":1,"data":"not-an-address"}]}`,
		`<html>перехватывающая заглушка</html>`,
	} {
		if got := parseDoHAnswer([]byte(body)); got != nil {
			t.Errorf("parseDoHAnswer(%q) = %v, want nil", body, got)
		}
	}
}

// Адрес возвращается без единого сетевого запроса: у большинства подписок
// сервер задан именно адресом, и поход к резолверу задерживал бы подключение
// ради заранее известного ответа. Проверяется отменённым контекстом — любой
// запрос по нему провалился бы.
func TestResolveHostPassesAddressesThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := ResolveHost(ctx, "198.51.100.4")
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if len(got) != 1 || !got[0].Equal(net.ParseIP("198.51.100.4")) {
		t.Fatalf("got %v, want 198.51.100.4", got)
	}
}

// Кеш существует ради «Пинговать все»: PingServer вызывается сразу на каждую
// ссылку подписки, и без кеша сотня узлов давала бы сотню DoH-запросов залпом.
// Отменённый контекст доказывает, что второй раз в сеть никто не пошёл.
func TestResolveHostServesFromCache(t *testing.T) {
	t.Cleanup(func() {
		resolveCacheMu.Lock()
		delete(resolveCache, "cached.example")
		resolveCacheMu.Unlock()
	})
	cacheHost("cached.example", []net.IP{net.ParseIP("203.0.113.9")})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := ResolveHost(ctx, "cached.example")
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if len(got) != 1 || !got[0].Equal(net.ParseIP("203.0.113.9")) {
		t.Fatalf("got %v, want the cached address", got)
	}

	// Отданный слайс переживает вызов. Если это сам слайс из кеша, правка у одного
	// вызывающего досталась бы всем следующим.
	got[0] = net.ParseIP("198.51.100.1")
	again, _ := ResolveHost(ctx, "cached.example")
	if !again[0].Equal(net.ParseIP("203.0.113.9")) {
		t.Errorf("кеш отдаёт свой слайс наружу: got %v", again)
	}
}

// Протухшая запись должна уходить в сеть, а не обслуживаться вечно.
func TestResolveHostIgnoresExpiredCache(t *testing.T) {
	resolveCacheMu.Lock()
	resolveCache["stale.example"] = resolveCacheEntry{
		ips:     []net.IP{net.ParseIP("203.0.113.9")},
		expires: time.Now().Add(-time.Second),
	}
	resolveCacheMu.Unlock()
	t.Cleanup(func() {
		resolveCacheMu.Lock()
		delete(resolveCache, "stale.example")
		resolveCacheMu.Unlock()
	})

	if got := cachedHost("stale.example"); got != nil {
		t.Fatalf("cachedHost вернул протухшее: %v", got)
	}
}

// Гонка обязана завершаться, даже когда не отвечает никто: проигравшая горутина
// пишет в буферизованный канал, и повиснуть на отправке ей негде.
func TestDoHRaceReturnsWhenNobodyAnswers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan []net.IP, 1)
	go func() { done <- dohRace(ctx, "unreachable.invalid") }()

	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dohRace не вернулся")
	}
}

// Свой DoH-резолвер, заданный именем, — последнее место на пути подключения,
// где имя уходило системному резолверу обычным запросом на 53-й порт. Адрес
// подставляется, SNI остаётся именем, domain_resolver исчезает за ненадобностью.
func TestResolveRemoteDNSHostReplacesTheName(t *testing.T) {
	t.Cleanup(func() {
		resolveCacheMu.Lock()
		delete(resolveCache, "doh.example")
		resolveCacheMu.Unlock()
	})
	cacheHost("doh.example", []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("203.0.113.5")})

	remote, err := remoteDNSServer("https://doh.example/dns-query", "proxy")
	if err != nil {
		t.Fatalf("remoteDNSServer: %v", err)
	}
	if remote["domain_resolver"] != "dns-direct" {
		t.Fatalf("предусловие не выполнено: domain_resolver = %v", remote["domain_resolver"])
	}

	resolveRemoteDNSHost(context.Background(), remote)

	if got := remote["server"]; got != "203.0.113.5" {
		t.Errorf("server = %v, want the IPv4 address", got)
	}
	if _, ok := remote["domain_resolver"]; ok {
		t.Error("domain_resolver остался — значит имя всё ещё ищут через 53-й порт")
	}
	tlsOpts, _ := remote["tls"].(map[string]interface{})
	if got := tlsOpts["server_name"]; got != "doh.example" {
		t.Errorf("SNI = %v, want doh.example — по нему проверяется сертификат", got)
	}
}

// Встроенный резолвер задан адресом и domain_resolver не несёт: трогать его
// нечего, и сетевого запроса тут быть не должно.
func TestResolveRemoteDNSHostLeavesBuiltinsAlone(t *testing.T) {
	remote, err := remoteDNSServer("8.8.8.8", "proxy")
	if err != nil {
		t.Fatalf("remoteDNSServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolveRemoteDNSHost(ctx, remote)

	if got := remote["server"]; got != "8.8.8.8" {
		t.Errorf("server = %v, want 8.8.8.8", got)
	}
}

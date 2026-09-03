package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Разрешение имени VPN-сервера до того, как туннель поднят.
//
// Раньше это делал системный резолвер, и он остаётся последней надеждой, но
// первым его спрашивать больше нельзя. Провайдеры в России перехватывают
// обычный DNS на порту 53 — включая запросы, адресованные 8.8.8.8 и 1.1.1.1, —
// и отвечают из НСДИ вместо адресата. Подменённый ответ здесь не «утечка
// приватности», а невозможность подключиться: приложение честно соединяется с
// адресом, который ему назвали, и попадает не туда, а причина выглядит как
// «сервер не отвечает».
//
// DoH этого перехвата не переживает только тогда, когда оператор рвёт и его —
// тогда работает откат на системный резолвер, то есть ровно прежнее поведение.
// Хуже, чем было, не становится нигде.

// bootstrapDoH — резолверы для этого одного запроса.
//
// Адресуются по IP, имя из сертификата уходит в SNI: искать адрес самого
// резолвера через DNS было бы тем же незащищённым запросом, ради которого всё
// и затевалось. Два провайдера, а не один, потому что блокировки у операторов
// разные и второй нередко жив, когда первый уже нет.
var bootstrapDoH = []struct{ ip, sni, endpoint string }{
	{"1.1.1.1", "cloudflare-dns.com", "https://cloudflare-dns.com/dns-query"},
	{"8.8.8.8", "dns.google", "https://dns.google/resolve"},
}

// dohClients живут столько же, сколько процесс.
//
// Transport на каждый вызов означал бы новое TLS-рукопожатие и брошенное
// соединение при каждой проверке kill switch и каждом пинге сервера. Приложение
// сутками висит в трее — накапливалось бы.
var dohClients = func() []*http.Client {
	clients := make([]*http.Client, len(bootstrapDoH))
	for i, provider := range bootstrapDoH {
		addr := net.JoinHostPort(provider.ip, "443")
		clients[i] = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
				TLSClientConfig: &tls.Config{
					ServerName: provider.sni,
					MinVersion: tls.VersionTLS12,
				},
				MaxIdleConns:    1,
				IdleConnTimeout: 30 * time.Second,
			},
		}
	}
	return clients
}()

// dohAttemptTimeout ограничивает один запрос к одному резолверу.
//
// Нужен отдельно от общего срока: резолверы опрашиваются разом, но молчат они
// тоже разом, и без своего предела зависший запрос доедал бы бюджет вызова до
// конца — на системный резолвер, последнюю надежду, не осталось бы ничего.
const dohAttemptTimeout = 1200 * time.Millisecond

// resolveCache хранит уже разрешённые имена.
//
// Без него «Пинговать все» на подписке из сотни узлов даёт сотню DoH-запросов
// подряд: PingServer вызывается на каждую ссылку сразу, и у системного резолвера
// такой залп гасил кеш ОС, а у DoH гасить нечем. К тому же одно и то же имя
// спрашивают трижды за подключение — сборка конфига, kill switch и пинг.
//
// ponytail: TTL фиксированный, вместо TTL из самого ответа — разбирать его ради
// имени VPN-сервера, которое живёт годами, нечего. Вытеснение — «выбросить
// протухшее, а если не помогло, очистить всё»: подписок длиннее предела почти
// не бывает, а LRU здесь стоил бы больше кода, чем экономил запросов.
const (
	resolveCacheTTL = 5 * time.Minute
	resolveCacheMax = 512
)

type resolveCacheEntry struct {
	ips     []net.IP
	expires time.Time
}

var (
	resolveCacheMu sync.Mutex
	resolveCache   = map[string]resolveCacheEntry{}
)

func cachedHost(host string) []net.IP {
	resolveCacheMu.Lock()
	defer resolveCacheMu.Unlock()
	entry, ok := resolveCache[host]
	if !ok || time.Now().After(entry.expires) {
		return nil
	}
	// Копия: слайс из кеша переживает вызов, и правка у одного вызывающего
	// иначе досталась бы всем остальным.
	return append([]net.IP(nil), entry.ips...)
}

func cacheHost(host string, ips []net.IP) {
	resolveCacheMu.Lock()
	defer resolveCacheMu.Unlock()
	if len(resolveCache) >= resolveCacheMax {
		now := time.Now()
		for key, entry := range resolveCache {
			if now.After(entry.expires) {
				delete(resolveCache, key)
			}
		}
		if len(resolveCache) >= resolveCacheMax {
			clear(resolveCache)
		}
	}
	resolveCache[host] = resolveCacheEntry{
		ips:     append([]net.IP(nil), ips...),
		expires: time.Now().Add(resolveCacheTTL),
	}
}

// ResolveHost разрешает имя через DoH, а если ни один резолвер не ответил — через
// системный.
//
// Готовый IP возвращается как есть: у подписок адрес чаще всего именно такой, и
// сетевой запрос ради него был бы ни к чему.
func ResolveHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if host == "" {
		return nil, errors.New("resolve: empty host")
	}
	if ips := cachedHost(host); ips != nil {
		return ips, nil
	}

	if ips := dohRace(ctx, host); len(ips) > 0 {
		cacheHost(host, ips)
		return ips, nil
	}

	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	// Кешируется и системный ответ: он мог прийти из-за того, что DoH у оператора
	// вырезан, и тогда следующий вызов иначе снова ждал бы оба таймаута впустую.
	// Неудача не кешируется вовсе — сеть чинится, и запоминать это незачем.
	cacheHost(host, addrs)
	return addrs, nil
}

// dohRace спрашивает оба резолвера сразу и берёт первый непустой ответ.
//
// По очереди было бы вдвое дольше ровно там, где это заметно: когда оператор
// вырезал первый резолвер, его таймаут целиком доставался пользователю, ждущему
// подключения. Блокировки у операторов разные, и живым оказывается то один, то
// другой — угадать порядок заранее нельзя.
func dohRace(ctx context.Context, host string) []net.IP {
	ctx, cancel := context.WithCancel(ctx)
	// Отменяет запрос проигравшего: без этого его соединение висело бы до своего
	// таймаута уже после того, как ответ получен и никому не нужен.
	defer cancel()

	// Буфер на всех: проигравший должен дописать результат и завершиться, а не
	// остаться висеть на отправке в никуда.
	results := make(chan []net.IP, len(bootstrapDoH))
	for i := range bootstrapDoH {
		go func(provider int) { results <- dohLookup(ctx, provider, host) }(i)
	}
	for range bootstrapDoH {
		if ips := <-results; len(ips) > 0 {
			return ips
		}
	}
	return nil
}

// dohLookup спрашивает у одного резолвера A и AAAA. Ошибки не возвращаются:
// вызывающий на неудачу одного провайдера может только перейти к следующему, а
// причина неудачи ни на что не влияет.
func dohLookup(ctx context.Context, provider int, host string) []net.IP {
	var ips []net.IP
	for _, qtype := range []string{"A", "AAAA"} {
		attemptCtx, cancel := context.WithTimeout(ctx, dohAttemptTimeout)
		body, err := dohQuery(attemptCtx, provider, host, qtype)
		cancel()
		if err != nil {
			// A не прошёл — AAAA по тому же пути не пройдёт тем более.
			return ips
		}
		ips = append(ips, parseDoHAnswer(body)...)
	}
	return ips
}

func dohQuery(ctx context.Context, provider int, host, qtype string) ([]byte, error) {
	query := url.Values{"name": {host}, "type": {qtype}}
	target := bootstrapDoH[provider].endpoint + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Cloudflare без этого заголовка отвечает wire-форматом, который пришлось бы
	// разбирать вручную; Google отдаёт JSON и так.
	req.Header.Set("Accept", "application/dns-json")

	resp, err := dohClients[provider].Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh: %s", resp.Status)
	}
	// Ответ на один вопрос — килобайты. Лимит стоит на случай, когда на 443 порту
	// оказался не резолвер, а перехватывающий прокси с большой страницей-заглушкой.
	return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
}

// parseDoHAnswer достаёт адреса из JSON-ответа DoH.
//
// CNAME и прочие записи в Answer идут вперемешку с адресными, поэтому отбор
// идёт по типу, а не по позиции: net.ParseIP на имени вернул бы nil, но полагаться
// на это значило бы принимать за адрес всё, что на него похоже.
func parseDoHAnswer(body []byte) []net.IP {
	var parsed struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Status != 0 {
		return nil
	}
	var ips []net.IP
	for _, answer := range parsed.Answer {
		if answer.Type != 1 && answer.Type != 28 { // A, AAAA
			continue
		}
		if ip := net.ParseIP(answer.Data); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

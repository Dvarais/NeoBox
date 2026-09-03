package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"NeoBox/backend/core"
)

// Watchdog: detects a dropped tunnel and reconnects, with backoff.

// Watchdog tuning.
//
// The cooldown starts at watchdogInitialBackoff and doubles up to
// watchdogMaxBackoff after every recovery attempt that could not succeed, so a
// machine left without upstream (router down after a power cut, cable pulled)
// is not subjected to an endless 45-second cycle of core teardowns and rebuilds.
const (
	watchdogProbeInterval   = 15 * time.Second
	watchdogFailThreshold   = 3
	watchdogInitialBackoff  = 30 * time.Second
	watchdogMaxBackoff      = 5 * time.Minute
	watchdogUpstreamTimeout = 5 * time.Second

	// Сколько запасных серверов опрашивать за одну попытку и сколько ждать
	// ответа. Опрос идёт параллельно, поэтому цена попытки — один таймаут, а не
	// двенадцать. Потолок нужен из-за подписок на сотни узлов: опрашивать их
	// все значило бы устроить всплеск исходящих соединений ровно в тот момент,
	// когда со связью и так плохо.
	watchdogMaxCandidates    = 12
	watchdogCandidateTimeout = 4 * time.Second

	// Сколько перезапусков ядра подряд считать доказательством того, что дело в
	// сервере, а не в связи.
	//
	// Проверка выше спрашивает у сервера только одно: принимает ли он
	// TCP-соединение. Этого достаточно, чтобы отличить мёртвую сеть от мёртвого
	// узла, но не достаточно, чтобы считать узел рабочим. Сервер, до которого
	// доходит рукопожатие, но через который не идёт трафик, — это не выдуманный
	// случай, а самый частый способ, которым узел перестаёт работать: истёк
	// оплаченный период, DPI научился резать именно этот отпечаток, ключ
	// отозвали. Порт при этом продолжает отвечать.
	//
	// В таком состоянии проверка живости проходит, watchdog доходит до
	// перезапуска, ядро поднимается «успешно» — и через 45 секунд всё
	// повторяется. Переключения не наступало никогда: до него доходило только
	// молчание сервера. Счётчик закрывает эту дыру: два перезапуска подряд, ни
	// один из которых не вернул локальный прокси к жизни, — повод спросить
	// остальные узлы, даже если этот отвечает.
	watchdogFutileRestarts = 2
)

// startWatchdog probes the local SOCKS proxy port every 15 s.
// After 3 consecutive failures it restarts the VPN core — but only once the VPN
// server is actually reachable again, see the upstream check below.
func (s *AppService) startWatchdog(link string, useSystemProxy bool) {
	// This goroutine drives core restarts, so a panic in it would take the whole
	// application down (the sing-box core shares this process). Record it rather
	// than letting the window disappear silently.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[watchdog] recovered from panic: %v\n%s\n", r, debug.Stack())
		}
	}()

	s.watchdogMu.Lock()
	// Cancel any previous watchdog before starting a new one
	if s.cancelWatchdog != nil {
		s.cancelWatchdog()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelWatchdog = cancel
	s.watchdogLink = link
	s.watchdogProxy = useSystemProxy
	s.watchdogMu.Unlock()

	// Address of the VPN server itself. It is the only meaningful upstream probe
	// available here: the Kill Switch explicitly allows it, and sing-box keeps it
	// routed outside the tunnel, so it answers exactly when real connectivity is
	// back. A generic probe (8.8.8.8 and friends) would be useless — in TUN mode
	// it goes through the dead tunnel, and under the Kill Switch it is blocked
	// outright.
	upstream := upstreamProbeAddr(link)

	// Give the VPN core time to fully initialise before probing
	select {
	case <-time.After(20 * time.Second):
	case <-ctx.Done():
		return
	}

	failCount := 0
	backoff := watchdogInitialBackoff
	var retryAfter time.Time // zero value == no cooldown in effect
	// Перезапуски ядра подряд, ни один из которых не вернул локальный прокси.
	// Обнуляется, как только прокси ответил, — см. watchdogFutileRestarts.
	futileRestarts := 0
	// Серверы, уже не ответившие в этом цикле. Без этого переключение ходило бы
	// по кругу между мёртвыми узлами. Сбрасывается на успешном подключении:
	// сервер, лежавший час назад, мог подняться.
	tried := map[string]bool{}
	ticker := time.NewTicker(watchdogProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", core.ProxyListenAddr, 3*time.Second)
			if err == nil {
				_ = conn.Close()
				failCount = 0
				backoff = watchdogInitialBackoff
				retryAfter = time.Time{}
				// Прокси отвечает — значит предыдущий перезапуск (если он был)
				// своё дело сделал, и обвинять текущий сервер не в чем.
				futileRestarts = 0
				// Здесь же забываются прежние неудачи других серверов: с тех пор
				// прошло время, и они могли подняться.
				//
				// Раньше сброс стоял на успехе startCore — то есть на факте
				// «процесс запустился». Это не то же самое, что работающее
				// соединение: на мёртвом узле ядро поднимается прекрасно, и
				// список испробованных обнулялся ровно тогда, когда он нужнее
				// всего. Единственное честное доказательство, что связь есть, —
				// вот эта проба, и сброс принадлежит ей.
				tried = map[string]bool{}
				continue
			}
			failCount++
			if failCount >= watchdogFailThreshold {
				// A previous attempt failed recently — stay quiet until it cools
				// down instead of retrying on every tick.
				if time.Now().Before(retryAfter) {
					continue
				}

				// Do not tear down and rebuild the core while the machine has no
				// upstream at all. Recreating sing-box (TUN adapter, interface
				// monitor, DNS transports) against a dead network cannot succeed,
				// and repeating it is what turns a brief outage into a crash loop.
				serverAnswers := upstream == "" || probeUpstream(ctx, upstream)

				// Сменили ли мы узел на этом такте. От этого зависит, считается
				// ли перезапуск ниже началом новой сессии.
				switched := false

				switch {
				case !serverAnswers:
					// Сервер молчит. Раньше здесь начиналось ожидание — и на этом
					// всё заканчивалось: при живой сети и заблокированном сервере
					// watchdog ждал его вечно, с нарастающей паузой, хотя рядом в
					// подписке лежали рабочие узлы. До перезапуска дело просто не
					// доходило, потому что continue уводил обратно в цикл.
					//
					// Теперь молчание сервера — повод спросить остальных.
					if alt := s.switchToLiveServer(ctx, tried); alt != "" {
						upstream = upstreamProbeAddr(alt)
						backoff = watchdogInitialBackoff
						futileRestarts = 0
						switched = true
						// Ниже по коду идёт перезапуск — он подхватит новую ссылку.
					} else {
						// Не ответил никто: это отсутствие сети, а не смерть
						// сервера. Переключаться некуда, ждём.
						s.emitSafe("watchdog-waiting")
						retryAfter = time.Now().Add(backoff)
						backoff = nextWatchdogBackoff(backoff)
						continue
					}

				case futileRestarts >= watchdogFutileRestarts:
					// Сервер отвечает на TCP, но перезапуски ядра прокси не
					// вернули. Значит проблема не в связи и не в ядре, а в самом
					// узле — он принимает соединение и ничего через себя не
					// пропускает. Ещё один перезапуск ничего не изменит.
					if alt := s.switchToLiveServer(ctx, tried); alt != "" {
						upstream = upstreamProbeAddr(alt)
						backoff = watchdogInitialBackoff
						futileRestarts = 0
						switched = true
					} else {
						// Запасных нет. Перезапускать этот узел ещё раз незачем:
						// два предыдущих раза не помогли, третий не поможет тем
						// более. Ждём — и говорим об этом отдельным событием, а
						// не общим «нет сети»: сеть-то как раз есть, и человеку
						// важно знать, что менять надо сервер, а не провайдера.
						//
						// Счётчик обнуляется вместе с паузой: после неё попытка
						// повторяется с чистого листа, потому что за это время
						// узел мог подняться, а запасные — вернуться в сеть.
						s.emitSafe("watchdog-server-dead")
						retryAfter = time.Now().Add(backoff)
						backoff = nextWatchdogBackoff(backoff)
						futileRestarts = 0
						continue
					}
				}

				failCount = 0
				// Считается здесь, до перезапуска, а не по его итогу: успех
				// startCore говорит лишь о том, что процесс поднялся, а работает
				// ли через него трафик, покажет только проба прокси на следующем
				// такте. Она же счётчик и обнулит.
				futileRestarts++
				s.emitSafe("watchdog-reconnecting")
				s.watchdogMu.Lock()
				savedLink := s.watchdogLink
				savedProxy := s.watchdogProxy
				s.watchdogMu.Unlock()
				// Restart without touching system proxy backup or kill switch
				_ = s.coreManager.Stop()
				s.stateMu.Lock()
				if s.cancelMonitor != nil {
					s.cancelMonitor()
					s.cancelMonitor = nil
				}
				s.stateMu.Unlock()

				// Verify we haven't been cancelled (disconnected by user) while stopping the core
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Переподключение к тому же серверу сессию не прерывает: «За
				// сессию» продолжает считать с того же места. А вот переход на
				// другой узел — прерывает, и обе стороны обязаны считать
				// одинаково: интерфейс по событию watchdog-switched закрывает
				// запись в «Истории» и начинает новую, и если бы счётчики Go
				// при этом продолжали расти, «За сессию» и подсказка в трее
				// показывали бы новому серверу трафик прошлого.
				res := s.startCore(savedLink, "", savedProxy, switched)
				if ok, _ := res["success"].(bool); ok {
					s.emitSafe("watchdog-reconnected")
					backoff = watchdogInitialBackoff
					retryAfter = time.Time{}
				} else {
					errMsg, _ := res["error"].(string)
					s.emitSafe("watchdog-failed", errMsg)
					retryAfter = time.Now().Add(backoff)
					backoff = nextWatchdogBackoff(backoff)
				}
			}
		}
	}
}

// switchToLiveServer подбирает работающий сервер взамен текущего и делает его
// текущим. Возвращает выбранную ссылку либо пустую строку, если менять не на
// что.
//
// Текущий сервер попадает в exclude здесь, а не у вызывающего: обе причины
// переключения — молчащий узел и узел, который отвечает, но не пропускает
// трафик, — одинаково означают «на этот возвращаться незачем», и забыть об
// этом в одной из двух веток было бы слишком легко.
func (s *AppService) switchToLiveServer(ctx context.Context, exclude map[string]bool) string {
	s.watchdogMu.Lock()
	exclude[s.watchdogLink] = true
	s.watchdogMu.Unlock()

	alt := pickLiveServer(ctx, s.watchdogCandidates(exclude))
	if alt == "" {
		return ""
	}

	s.watchdogMu.Lock()
	s.watchdogLink = alt
	s.watchdogMu.Unlock()

	// Фронтенд закрывает прежнюю сессию в истории, открывает новую и показывает
	// уведомление: подмена сервера обязана быть событием, а не тихой заменой
	// под пользователем.
	//
	// Итоги закрываемой сессии уезжают вместе с событием, а не спрашиваются
	// потом отдельным вызовом: перезапуск ниже обнулит счётчики как раз между
	// событием и таким вопросом, и запись в «Историю» получила бы нули вместо
	// трафика того сервера, на котором он был набран.
	up, down := s.sessionTraffic()
	s.emitSafe("watchdog-switched", alt, map[string]int64{"up": up, "down": down})
	return alt
}

// watchdogCandidates возвращает ссылки, которые имеет смысл попробовать вместо
// переставшего отвечать сервера.
//
// Порядок — тот, в котором серверы лежат в подписках; выбор между ними делает
// не порядок, а опрос: отвечает первый — значит он и ближайший. Замеры пинга
// сюда не годятся, они живут во фронтенде и на момент аварии всё равно
// устарели.
//
// exclude — уже испробованные и не ответившие в этом же цикле, чтобы
// переключение не ходило по кругу между мёртвыми узлами.
func (s *AppService) watchdogCandidates(exclude map[string]bool) []string {
	var subs []Subscription
	if err := json.Unmarshal([]byte(s.GetSubscriptions()), &subs); err != nil {
		return nil
	}

	seen := make(map[string]bool, len(exclude))
	for link := range exclude {
		seen[link] = true
	}

	var out []string
	for _, sub := range subs {
		for _, link := range sub.Links {
			link = strings.TrimSpace(link)
			if link == "" || seen[link] {
				continue
			}
			seen[link] = true
			out = append(out, link)
			if len(out) >= watchdogMaxCandidates {
				return out
			}
		}
	}
	return out
}

// pickLiveServer опрашивает кандидатов параллельно и возвращает ссылку на
// первого ответившего.
//
// Здесь же решается вопрос, который иначе потребовал бы отдельной эвристики:
// умерла сеть или умер сервер. Если не отвечает никто — сеть, и переключаться
// некуда; если ответил хоть кто-то — дело в сервере. Различать по чему-то
// внешнему (пинг до 8.8.8.8) нельзя: в TUN-режиме такой запрос уходит в
// мёртвый туннель, а под Kill Switch блокируется.
func pickLiveServer(ctx context.Context, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}

	probeCtx, cancel := context.WithTimeout(ctx, watchdogCandidateTimeout)
	defer cancel()

	found := make(chan string, len(candidates))
	var wg sync.WaitGroup

	for _, link := range candidates {
		addr := upstreamProbeAddr(link)
		if addr == "" {
			continue
		}
		wg.Add(1)
		go func(link, addr string) {
			defer wg.Done()
			var dialer net.Dialer
			conn, err := dialer.DialContext(probeCtx, "tcp", addr)
			if err != nil {
				return
			}
			_ = conn.Close()
			// Буфер рассчитан на всех, поэтому отправка не заблокируется даже
			// если победитель уже определён и читать никто не будет.
			found <- link
		}(link, addr)
	}

	go func() {
		wg.Wait()
		close(found)
	}()

	// Первый ответивший и есть выбор: он же и самый быстрый из доступных.
	for link := range found {
		cancel()
		return link
	}
	return ""
}

// upstreamProbeAddr extracts the VPN server's host:port from a proxy link.
// Returns "" when the link yields no usable address, in which case the watchdog
// falls back to restarting unconditionally (the previous behaviour).
func upstreamProbeAddr(link string) string {
	outbound, err := core.ParseProxyLink(link)
	if err != nil {
		return ""
	}
	// ServerEndpoint, not outbound["server"]: a WireGuard endpoint keeps its
	// address in the first peer, so the direct read yielded nothing for it and
	// the watchdog silently lost its upstream check on every WireGuard node.
	host, port := core.ServerEndpoint(outbound)
	if host == "" || port <= 0 || port > 65535 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// probeUpstream reports whether the VPN server accepts a direct TCP connection,
// which is the signal that the machine has real connectivity again.
func probeUpstream(ctx context.Context, addr string) bool {
	dialCtx, cancel := context.WithTimeout(ctx, watchdogUpstreamTimeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// nextWatchdogBackoff doubles the cooldown up to watchdogMaxBackoff.
func nextWatchdogBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > watchdogMaxBackoff {
		return watchdogMaxBackoff
	}
	return next
}

// stopWatchdog cancels the running watchdog goroutine if any.
func (s *AppService) stopWatchdog() {
	s.watchdogMu.Lock()
	defer s.watchdogMu.Unlock()
	if s.cancelWatchdog != nil {
		s.cancelWatchdog()
		s.cancelWatchdog = nil
	}
}

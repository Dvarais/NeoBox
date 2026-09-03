package service

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"NeoBox/backend/i18n"
)

// Системные горячие клавиши: подключиться и показать окно, не разворачивая
// приложение.
//
// Смысл ровно в сценарии из PRODUCT.md: приложение живёт в трее, и большинство
// сессий — одно действие. Пока это действие требует найти иконку в трее и
// попасть по ней мышью, оно стоит дороже, чем само подключение.
//
// Клавиши по умолчанию выключены и включаются галкой в «Настройках». Это не
// осторожность ради осторожности: RegisterHotKey забирает сочетание у всей
// системы, и приложение, которое молча отбирает Ctrl+Shift+V у того, кто им уже
// пользуется, ведёт себя хуже, чем приложение без горячих клавиш вовсе.
//
// Сами сочетания задаются в настройках. Захардкоженная пара была той же
// проблемой с другой стороны: занято — и починить это можно было только
// отказавшись от горячих клавиш целиком.
//
// Реализация упирается в одно свойство RegisterHotKey: с нулевым HWND сообщение
// WM_HOTKEY приходит в очередь ПОТОКА, который регистрировал. Поэтому здесь
// отдельная горутина, прибитая к своему потоку через runtime.LockOSThread, и
// свой цикл GetMessage. Взять для этого поток Wails нельзя — его цикл
// сообщений принадлежит WebView2, и вклиниваться в него ради двух сочетаний
// значило бы отвечать за чужие сообщения.

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procGetMessage       = user32.NewProc("GetMessageW")
	procPostThreadMsg    = user32.NewProc("PostThreadMessageW")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThrID  = kernel32.NewProc("GetCurrentThreadId")
)

// Модификаторы RegisterHotKey.
const (
	modAlt     = 0x0001
	modControl = 0x0002
	modShift   = 0x0004
	modWin     = 0x0008
	// modNoRepeat не даёт удержанию клавиш сыпать сообщениями: без него
	// зажатое сочетание переключало бы подключение десятки раз в секунду.
	modNoRepeat = 0x4000
)

// Сообщения оконной очереди.
const (
	wmHotkey = 0x0312
	// wmQuitLoop — своё сообщение для остановки цикла. WM_QUIT (0x0012) через
	// PostThreadMessage дошёл бы тоже, но он же приходит при завершении
	// процесса, и различать «нас выключили в настройках» и «приложение
	// закрывается» стало бы невозможно.
	wmQuitLoop = 0x0400 + 1
)

// Идентификаторы сочетаний. Произвольные, но обязаны быть различны в пределах
// потока: UnregisterHotKey адресуется именно ими.
const (
	hotkeyToggleID = 1
	hotkeyShowID   = 2
)

// Сочетания по умолчанию. Ctrl+Shift+V — «VPN», Ctrl+Shift+B — «Box», окно.
//
// Выбор пал на трёхклавишные с Ctrl+Shift намеренно: двухклавишные сочетания в
// Windows почти все заняты, а Win+клавиша принадлежит системе и часть из них
// перехватить нельзя в принципе. Пользователь волен выбрать другие.
const (
	defaultHotkeyToggle = "Ctrl+Shift+V"
	defaultHotkeyShow   = "Ctrl+Shift+B"
)

type hotkeyBinding struct {
	id  int
	mod uintptr
	vk  uintptr
	// event — что отправить фронтенду при нажатии.
	event string
}

// buildHotkeyBindings разбирает пару сочетаний из настроек. Возвращает причину
// отказа строкой, потому что она уходит прямо в диалог: сочетание, которое
// приложение не поняло, обязано быть названо.
func buildHotkeyBindings(toggleCombo, showCombo string) ([]hotkeyBinding, string) {
	specs := []struct {
		id                     int
		combo, fallback, event string
	}{
		{hotkeyToggleID, toggleCombo, defaultHotkeyToggle, "hotkey-toggle-connection"},
		{hotkeyShowID, showCombo, defaultHotkeyShow, "hotkey-show-window"},
	}

	var bindings []hotkeyBinding
	for _, spec := range specs {
		combo := strings.TrimSpace(spec.combo)
		if combo == "" {
			combo = spec.fallback
		}
		mod, vk, ok := parseHotkey(combo)
		if !ok {
			return nil, i18n.T(i18n.ErrHotkeyInvalid, combo)
		}
		bindings = append(bindings, hotkeyBinding{id: spec.id, mod: mod, vk: vk, event: spec.event})
	}
	return bindings, ""
}

// parseHotkey превращает «Ctrl+Shift+V» в маску модификаторов и код клавиши.
func parseHotkey(combo string) (mod, vk uintptr, ok bool) {
	parts := strings.Split(combo, "+")
	mod = modNoRepeat
	for i, part := range parts {
		name := strings.ToUpper(strings.TrimSpace(part))
		if name == "" {
			return 0, 0, false
		}
		if i < len(parts)-1 {
			switch name {
			case "CTRL", "CONTROL":
				mod |= modControl
			case "SHIFT":
				mod |= modShift
			case "ALT":
				mod |= modAlt
			case "WIN", "META":
				mod |= modWin
			default:
				return 0, 0, false
			}
			continue
		}
		if vk, ok = virtualKey(name); !ok {
			return 0, 0, false
		}
	}
	// Без модификатора RegisterHotKey забрал бы у системы голую клавишу, и та
	// перестала бы доходить до кого бы то ни было ещё.
	if mod == modNoRepeat {
		return 0, 0, false
	}
	return mod, vk, true
}

// virtualKey covers exactly what the key capture in the settings page can
// produce: letters, digits and the function row.
func virtualKey(name string) (uintptr, bool) {
	if len(name) == 1 {
		c := name[0]
		switch {
		case c >= 'A' && c <= 'Z':
			return uintptr(c), true // VK_A..VK_Z совпадают с ASCII
		case c >= '0' && c <= '9':
			return uintptr(c), true // VK_0..VK_9 тоже
		}
		return 0, false
	}
	if strings.HasPrefix(name, "F") {
		var n int
		if _, err := fmt.Sscanf(name, "F%d", &n); err == nil && n >= 1 && n <= 12 {
			return uintptr(0x6F + n), true // VK_F1 = 0x70
		}
	}
	return 0, false
}

// msg соответствует MSG из windows.h. Поля после lParam циклу не нужны, но
// присутствовать обязаны: GetMessage пишет в структуру целиком.
type msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       struct{ x, y int32 }
	lPrivate uint32
}

// hotkeyState — всё, что нужно, чтобы выключить горячие клавиши обратно.
type hotkeyState struct {
	mu       sync.Mutex
	running  bool
	threadID uintptr
	// stopped закрывается циклом на выходе. Без него смена сочетания при
	// включённых клавишах регистрировала бы новую пару раньше, чем старая
	// освободила прежнюю, и RegisterHotKey отказал бы на ровном месте.
	stopped chan struct{}
}

var hotkeys hotkeyState

// hotkeyStart — то, что горутина сообщает о себе при запуске: причина отказа
// либо поток, которому потом слать просьбу остановиться.
type hotkeyStart struct {
	threadID uintptr
	failure  string
}

// SetGlobalHotkeys включает или выключает системные горячие клавиши.
//
// Возвращает пустую строку при успехе и причину отказа, если хотя бы одно
// сочетание занято другим приложением. Причина возвращается, а не пишется в
// лог: молча не включившиеся горячие клавиши — это галка, которая стоит и не
// работает, то есть ровно то поведение, которое продукт себе запрещает.
func (s *AppService) SetGlobalHotkeys(enable bool, toggleCombo, showCombo string) string {
	hotkeys.mu.Lock()
	defer hotkeys.mu.Unlock()

	// Останавливаем всегда, а не только при выключении: сочетание — это
	// регистрация в системе, и подвинуть её можно единственным способом —
	// снять и взять заново.
	s.stopHotkeyLoop()
	if !enable {
		return ""
	}

	bindings, failure := buildHotkeyBindings(toggleCombo, showCombo)
	if failure != "" {
		return failure
	}

	// Канал на одно сообщение: горутина сообщает, поднялась ли она, и дальше в
	// него никто не пишет. Идентификатор потока едет тем же каналом, а не
	// присваиванием в hotkeys — иначе это была бы запись из одной горутины и
	// чтение из другой, которую детектор гонок справедливо считает гонкой,
	// сколько бы мьютексов ни стояло вокруг чтения.
	ready := make(chan hotkeyStart, 1)
	stopped := make(chan struct{})
	go s.hotkeyLoop(bindings, ready, stopped)

	started := <-ready
	if started.failure != "" {
		return started.failure
	}
	hotkeys.threadID = started.threadID
	hotkeys.stopped = stopped
	hotkeys.running = true
	return ""
}

// stopHotkeyLoop просит горутину выйти и дожидается, пока она снимет
// регистрации. Вызывается под hotkeys.mu.
func (s *AppService) stopHotkeyLoop() {
	if !hotkeys.running {
		return
	}
	if hotkeys.threadID != 0 {
		_, _, _ = procPostThreadMsg.Call(hotkeys.threadID, wmQuitLoop, 0, 0)
	}
	if hotkeys.stopped != nil {
		// Ограниченное ожидание: цикл сидит в GetMessage и выходит сразу, но
		// вешать на этом весь интерфейс из-за чужого зависшего потока нельзя.
		select {
		case <-hotkeys.stopped:
		case <-time.After(2 * time.Second):
			fmt.Println("[hotkey] message loop did not stop in time")
		}
	}
	hotkeys.threadID = 0
	hotkeys.stopped = nil
	hotkeys.running = false
}

// hotkeyLoop регистрирует сочетания и крутит цикл сообщений до остановки.
//
// Горутина прибита к своему потоку на всё время жизни: и регистрация, и снятие,
// и GetMessage обязаны идти из одного потока — иначе сообщения придут в чужую
// очередь, а UnregisterHotKey откажет.
func (s *AppService) hotkeyLoop(bindings []hotkeyBinding, ready chan<- hotkeyStart, stopped chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(stopped)

	threadID, _, _ := procGetCurrentThrID.Call()

	var registered []int
	for _, b := range bindings {
		ret, _, err := procRegisterHotKey.Call(0, uintptr(b.id), b.mod, b.vk)
		if ret == 0 {
			// Откатываем уже занятое: половина работающих сочетаний хуже, чем
			// ни одного, — пользователь не поймёт, какое из них живое.
			for _, id := range registered {
				_, _, _ = procUnregisterHotKey.Call(0, uintptr(id))
			}
			ready <- hotkeyStart{failure: fmt.Sprintf("%v", err)}
			return
		}
		registered = append(registered, b.id)
	}

	ready <- hotkeyStart{threadID: threadID}

	defer func() {
		for _, id := range registered {
			_, _, _ = procUnregisterHotKey.Call(0, uintptr(id))
		}
	}()

	var m msg
	for {
		// hwnd = 0 забирает сообщения всего потока, включая WM_HOTKEY, который
		// не принадлежит ни одному окну. Возврат 0 — это WM_QUIT, -1 — ошибка;
		// в обоих случаях крутить цикл дальше нельзя.
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 || int32(ret) == -1 {
			return
		}
		switch m.message {
		case wmQuitLoop:
			return
		case wmHotkey:
			for _, b := range bindings {
				if uintptr(b.id) == m.wParam {
					s.emitSafe(b.event)
					break
				}
			}
		}
	}
}

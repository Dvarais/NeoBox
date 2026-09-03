package service

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"NeoBox/backend/security"
	"NeoBox/backend/storage"
)

// Kill Switch lifecycle: arming, disarming and recovering leftover firewall rules.

// killSwitchMarkerPath marks that NeoBox currently has firewall rules installed.
//
// Windows Firewall rules outlive the process that created them. NeoBox can die
// without running its shutdown path — a panic in a sing-box goroutine, Task
// Manager, a forced update — and the block-all rule then survives the reboot,
// leaving the machine with NO internet at all and no obvious culprit. The marker
// is what lets the next start recognise that situation and clean up.
func (s *AppService) killSwitchMarkerPath() string {
	return filepath.Join(s.userDataDir, "killswitch.active")
}

// enableKillSwitch arms the kill switch and records that it is armed.
func (s *AppService) enableKillSwitch(serverHost string) error {
	if err := security.EnableKillSwitch(serverHost); err != nil {
		return err
	}
	if err := storage.WriteFile(s.killSwitchMarkerPath(), []byte(time.Now().Format(time.RFC3339))); err != nil {
		// The rules are live but unrecorded: a crash from here on would leave them
		// behind silently. Not worth refusing the connection over, but say so.
		fmt.Printf("[killswitch] warning: could not record active state, a crash will not be recoverable: %v\n", err)
	}
	return nil
}

// disableKillSwitch removes the firewall rules and clears the marker. The marker
// is kept when removal fails, so the next start tries again.
func (s *AppService) disableKillSwitch() {
	if err := security.DisableKillSwitch(); err != nil {
		fmt.Printf("[killswitch] %v\n", err)
		return
	}
	_ = os.Remove(s.killSwitchMarkerPath())
}

// recoverKillSwitch removes firewall rules left behind by a previous run that
// did not shut down cleanly. It runs at startup, before the user can be left
// wondering why nothing on the machine can reach the network.
func (s *AppService) recoverKillSwitch() {
	if _, err := os.Stat(s.killSwitchMarkerPath()); err != nil {
		return // no marker — previous run cleaned up after itself
	}

	fmt.Println("[killswitch] rules left over from a previous session — removing")
	if err := security.DisableKillSwitch(); err != nil {
		// Deleting firewall rules needs elevation. If NeoBox was elevated when it
		// crashed and is now started from the Run key (which is not elevated), the
		// rules cannot be removed and the user stays offline. Keep the marker so
		// the next start retries, and surface it in diagnostics.
		fmt.Printf("[killswitch] recovery FAILED, the machine may have no internet access: %v\n", err)
		// Раньше это оставалось только в crash-логе. Между тем это худшее
		// состояние продукта: машина без интернета, приложение ещё даже не
		// подключено, и связать одно с другим пользователю нечем. Запоминаем,
		// чтобы интерфейс мог сказать об этом вслух.
		s.stateMu.Lock()
		s.killSwitchStuck = true
		s.stateMu.Unlock()
		return
	}
	_ = os.Remove(s.killSwitchMarkerPath())
	fmt.Println("[killswitch] leftover rules removed")
}

// GetKillSwitchState сообщает, режет ли сейчас сеть сам NeoBox.
//
// Нужно это ровно затем, чтобы «интернета нет» не выглядело как поломка
// провайдера. Правила брандмауэра переживают процесс, который их поставил, и до
// сих пор единственным следом их существования был файл-маркер, о котором
// пользователь не знает.
//
// Возвращает карту, а не структуру, — так же, как GetConnections и StartXray:
// биндинги Wails здесь генерируются в Record<string, any>, и заводить ради двух
// булевых полей отдельную модель значило бы выбиваться из общего порядка.
//
//	active — правила установлены; считается по тому же маркеру, которым
//	         пользуется восстановление при старте;
//	stuck  — правила остались от прошлого запуска и снять их не удалось.
//	         Обычно это упавший сеанс с правами администратора и следующий
//	         запуск без них: машина без сети, и помогает только перезапуск с
//	         повышением прав.
func (s *AppService) GetKillSwitchState() map[string]interface{} {
	_, err := os.Stat(s.killSwitchMarkerPath())
	s.stateMu.Lock()
	stuck := s.killSwitchStuck
	s.stateMu.Unlock()
	return map[string]interface{}{
		"active": err == nil,
		"stuck":  stuck,
	}
}

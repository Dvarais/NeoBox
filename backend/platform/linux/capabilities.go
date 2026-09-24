package linux

import (
	"fmt"
	"os"
)

type PrivilegeManager struct{}

// CanRunTUN checks if TUN interface access (/dev/net/tun) is permitted.
func (m *PrivilegeManager) CanRunTUN() bool {
	if os.Geteuid() == 0 {
		return true
	}
	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
		return true
	}
	return false
}

// CanConfigureFirewall checks if the process can invoke firewall commands.
func (m *PrivilegeManager) CanConfigureFirewall() bool {
	return m.CanRunTUN()
}

// PrivilegeHelpMessage gives user-facing instructions to grant capabilities.
func (m *PrivilegeManager) PrivilegeHelpMessage() string {
	execPath, err := os.Executable()
	if err != nil {
		execPath = "/usr/bin/neobox"
	}
	return fmt.Sprintf("Для работы в режиме TUN требуются сетевые привилегии.\nВыполните в терминале:\nsudo setcap cap_net_admin,cap_net_bind_service+ep %s", execPath)
}

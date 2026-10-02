package linux

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type PrivilegeManager struct{}

// hasCapNetAdmin checks if the current process has CAP_NET_ADMIN (bit 12)
// in its effective or permitted capabilities set, or is running as root.
func hasCapNetAdmin() bool {
	if os.Geteuid() == 0 {
		return true
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	return ParseCapNetAdmin(string(data))
}

// ParseCapNetAdmin parses /proc/self/status content for CAP_NET_ADMIN (bit 12).
func ParseCapNetAdmin(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "CapEff:") || strings.HasPrefix(line, "CapPrm:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				val, err := strconv.ParseUint(parts[1], 16, 64)
				if err == nil && (val&(1<<12)) != 0 {
					return true
				}
			}
		}
	}
	return false
}

// CanRunTUN checks if TUN interface access is permitted via root or CAP_NET_ADMIN.
func (m *PrivilegeManager) CanRunTUN() bool {
	if !hasCapNetAdmin() {
		return false
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
	return hasCapNetAdmin()
}

// PrivilegeHelpMessage gives user-facing instructions to grant capabilities.
func (m *PrivilegeManager) PrivilegeHelpMessage() string {
	execPath, err := os.Executable()
	if err != nil {
		execPath = "/usr/bin/neobox"
	}
	return fmt.Sprintf("Для работы в режиме TUN требуются сетевые привилегии.\nВыполните в терминале:\nsudo setcap cap_net_admin,cap_net_bind_service+eip %s", execPath)
}

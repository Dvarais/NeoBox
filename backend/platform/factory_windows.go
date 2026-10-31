//go:build windows

package platform

import (
	"NeoBox/backend/platform/windows"
)

type windowsPlatform struct {
	paths      windows.PathManager
	security   windows.SecurityManager
	firewall   windows.FirewallManager
	proxy      windows.ProxyManager
	privileges windows.PrivilegeManager
	prober     windows.Prober
	lifecycle  windows.LifecycleManager
	dns        windows.DNSManager
	processes  windows.ProcessManager
}

func (p *windowsPlatform) Paths() PathManager           { return &p.paths }
func (p *windowsPlatform) Security() SecurityManager     { return &p.security }
func (p *windowsPlatform) Firewall() FirewallManager     { return &p.firewall }
func (p *windowsPlatform) Proxy() ProxyManager           { return &p.proxy }
func (p *windowsPlatform) Privileges() PrivilegeManager { return &p.privileges }
func (p *windowsPlatform) Prober() NetworkProber         { return &p.prober }
func (p *windowsPlatform) Lifecycle() LifecycleManager   { return &p.lifecycle }
func (p *windowsPlatform) DNS() DNSManager               { return &p.dns }
func (p *windowsPlatform) Processes() ProcessManager     { return &windowsProcessAdapter{mgr: &p.processes} }

type windowsProcessAdapter struct {
	mgr *windows.ProcessManager
}

func (a *windowsProcessAdapter) GetRunningProcesses() ([]ProcessInfo, error) {
	procs, err := a.mgr.GetRunningProcesses()
	if err != nil {
		return nil, err
	}
	res := make([]ProcessInfo, len(procs))
	for i, p := range procs {
		res[i] = ProcessInfo{
			Name:  p.Name,
			Path:  p.Path,
			Count: p.Count,
		}
	}
	return res, nil
}

func init() {
	SetCurrent(&windowsPlatform{})
}

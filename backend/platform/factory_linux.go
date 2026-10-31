//go:build linux

package platform

import (
	"NeoBox/backend/platform/linux"
)

type linuxPlatform struct {
	paths      linux.PathManager
	security   linux.SecurityManager
	firewall   linux.FirewallManager
	proxy      linux.ProxyManager
	privileges linux.PrivilegeManager
	prober     linux.Prober
	lifecycle  linux.LifecycleManager
	dns        linux.DNSManager
	processes  linux.ProcessManager
}

func (p *linuxPlatform) Paths() PathManager           { return &p.paths }
func (p *linuxPlatform) Security() SecurityManager     { return &p.security }
func (p *linuxPlatform) Firewall() FirewallManager     { return &p.firewall }
func (p *linuxPlatform) Proxy() ProxyManager           { return &p.proxy }
func (p *linuxPlatform) Privileges() PrivilegeManager { return &p.privileges }
func (p *linuxPlatform) Prober() NetworkProber         { return &p.prober }
func (p *linuxPlatform) Lifecycle() LifecycleManager   { return &p.lifecycle }
func (p *linuxPlatform) DNS() DNSManager               { return &p.dns }
func (p *linuxPlatform) Processes() ProcessManager     { return &linuxProcessAdapter{mgr: &p.processes} }

type linuxProcessAdapter struct {
	mgr *linux.ProcessManager
}

func (a *linuxProcessAdapter) GetRunningProcesses() ([]ProcessInfo, error) {
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
	p := &linuxPlatform{}
	userDataDir := p.paths.UserDataDir()
	p.proxy.SetUserDataDir(userDataDir)
	p.firewall.SetUserDataDir(userDataDir)
	SetCurrent(p)
}

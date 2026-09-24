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
}

func (p *linuxPlatform) Paths() PathManager           { return &p.paths }
func (p *linuxPlatform) Security() SecurityManager     { return &p.security }
func (p *linuxPlatform) Firewall() FirewallManager     { return &p.firewall }
func (p *linuxPlatform) Proxy() ProxyManager           { return &p.proxy }
func (p *linuxPlatform) Privileges() PrivilegeManager { return &p.privileges }
func (p *linuxPlatform) Prober() NetworkProber         { return &p.prober }
func (p *linuxPlatform) Lifecycle() LifecycleManager   { return &p.lifecycle }

func init() {
	p := &linuxPlatform{}
	p.proxy.SetUserDataDir(p.paths.UserDataDir())
	SetCurrent(p)
}

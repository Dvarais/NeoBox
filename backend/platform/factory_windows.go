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
}

func (p *windowsPlatform) Paths() PathManager           { return &p.paths }
func (p *windowsPlatform) Security() SecurityManager     { return &p.security }
func (p *windowsPlatform) Firewall() FirewallManager     { return &p.firewall }
func (p *windowsPlatform) Proxy() ProxyManager           { return &p.proxy }
func (p *windowsPlatform) Privileges() PrivilegeManager { return &p.privileges }
func (p *windowsPlatform) Prober() NetworkProber         { return &p.prober }
func (p *windowsPlatform) Lifecycle() LifecycleManager   { return &p.lifecycle }

func init() {
	SetCurrent(&windowsPlatform{})
}

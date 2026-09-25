//go:build !windows

package service

import (
	"NeoBox/backend/core"
	"NeoBox/backend/platform"
)

const systemProxyAddr = core.ProxyListenAddr

func (s *AppService) loadProxyBackup() {
	if p := platform.Current(); p != nil && p.Proxy() != nil {
		p.Proxy().RecoverSystemProxy(s.userDataDir)
	}
}

func (s *AppService) SetSystemProxy(enable bool) {
	p := platform.Current()
	if p == nil || p.Proxy() == nil {
		return
	}
	if enable {
		_ = p.Proxy().SetSystemProxy(systemProxyAddr)
	} else {
		_ = p.Proxy().RestoreSystemProxy()
	}
}

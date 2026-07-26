package core

// Loopback endpoints NeoBox binds. These were previously written out as literals
// in eight places across three packages — the generated sing-box config, the
// Windows proxy registry value, the watchdog probe, the traffic monitor and the
// diagnostics screen — where changing one and missing another silently breaks a
// feature rather than failing to compile.
//
// ProxyListenAddr and ClashAPIAddr must agree with the host/port pairs below;
// TestPortConstantsAgree enforces that, since Go cannot build the strings from
// the numbers at compile time.
const (
	// ProxyListenHost is the interface the local inbounds bind to. Loopback only:
	// these ports must never be reachable from the network.
	ProxyListenHost = "127.0.0.1"

	// ProxyListenPort is the "mixed" (SOCKS+HTTP) inbound that carries user traffic
	// and that the Windows system proxy setting points at.
	ProxyListenPort = 20809
	ProxyListenAddr = "127.0.0.1:20809"

	// ClashAPIPort is the sing-box Clash API used for live traffic statistics.
	// It is protected by a per-session secret — see GenerateConfig.
	ClashAPIPort = 9097
	ClashAPIAddr = "127.0.0.1:9097"
)

package service



// VPNState represents the lifecycle phase of the VPN connection.
type VPNState int32

const (
	VPNStateDisconnected VPNState = iota
	VPNStateConnecting
	VPNStateConnected
	VPNStateDisconnecting
	VPNStateReconnecting
)

// String returns human-readable representation of VPNState.
func (s VPNState) String() string {
	switch s {
	case VPNStateDisconnected:
		return "disconnected"
	case VPNStateConnecting:
		return "connecting"
	case VPNStateConnected:
		return "connected"
	case VPNStateDisconnecting:
		return "disconnecting"
	case VPNStateReconnecting:
		return "reconnecting"
	default:
		return "unknown"
	}
}

// GetVPNState returns the current VPN connection lifecycle state.
//
// [INTERNET SEARCH PATTERN]:
// Uses atomic.Int32 load to provide non-blocking, lock-free state reads
// by UI, tray, and connection monitors without thread parking or mutex contention.
func (s *AppService) GetVPNState() VPNState {
	return VPNState(s.vpnState.Load())
}

// setVPNState stores the new VPN state atomically.
func (s *AppService) setVPNState(st VPNState) {
	s.vpnState.Store(int32(st))
}

// casVPNState atomically compares and swaps state from expected to next.
// Returns true if swap was successful.
func (s *AppService) casVPNState(expected, next VPNState) bool {
	return s.vpnState.CompareAndSwap(int32(expected), int32(next))
}

// IsVPNConnected reports whether the VPN connection is in the Connected state.
func (s *AppService) IsVPNConnected() bool {
	return s.GetVPNState() == VPNStateConnected
}

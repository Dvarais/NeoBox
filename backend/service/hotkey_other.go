//go:build !windows

package service

// SetGlobalHotkeys on non-Windows is currently a stub.
func (s *AppService) SetGlobalHotkeys(enable bool, toggleCombo, showCombo string) string {
	return ""
}

package service

import "testing"

func newTestService(t *testing.T) *AppService {
	t.Helper()
	return &AppService{userDataDir: t.TempDir()}
}

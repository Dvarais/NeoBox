package service

import (
	"testing"
)

func TestGetRunningProcesses(t *testing.T) {
	s := &AppService{}
	procs, err := s.GetRunningProcesses()
	if err != nil {
		t.Fatalf("GetRunningProcesses failed: %v", err)
	}

	if len(procs) == 0 {
		t.Fatal("expected non-empty process list")
	}

	for _, p := range procs {
		if p.Name == "" {
			t.Errorf("found process with empty Name: %+v", p)
		}
		if p.Count <= 0 {
			t.Errorf("found process with non-positive Count: %+v", p)
		}
	}
}

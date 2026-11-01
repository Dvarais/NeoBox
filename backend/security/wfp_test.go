package security

import "testing"

func TestWFPInitialState(t *testing.T) {
	if IsWFPActive() {
		t.Fatalf("expected WFP to be inactive initially")
	}
}

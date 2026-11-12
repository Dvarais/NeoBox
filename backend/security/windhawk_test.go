package security

import (
	"strings"
	"testing"
)

func TestMergeExclusions(t *testing.T) {
	tests := []struct {
		name         string
		existing     string
		targets      []string
		wantModified bool
		wantContains []string
	}{
		{
			name:         "empty existing string",
			existing:     "",
			targets:      []string{"NeoBox.exe", "msedgewebview2.exe"},
			wantModified: true,
			wantContains: []string{"NeoBox.exe", "msedgewebview2.exe"},
		},
		{
			name:         "existing already contains all targets exact match",
			existing:     "NeoBox.exe|msedgewebview2.exe",
			targets:      []string{"NeoBox.exe", "msedgewebview2.exe"},
			wantModified: false,
			wantContains: []string{"NeoBox.exe", "msedgewebview2.exe"},
		},
		{
			name:         "existing already contains all targets case-insensitively",
			existing:     "neobox.exe|MSEDGEWEBVIEW2.EXE",
			targets:      []string{"NeoBox.exe", "msedgewebview2.exe"},
			wantModified: false,
			wantContains: []string{"neobox.exe", "MSEDGEWEBVIEW2.EXE"},
		},
		{
			name:         "existing has third-party apps, appends missing targets",
			existing:     "javaw.exe|Photoshop.exe|Harem Heaven.exe",
			targets:      []string{"NeoBox.exe", "msedgewebview2.exe"},
			wantModified: true,
			wantContains: []string{"javaw.exe", "Photoshop.exe", "Harem Heaven.exe", "NeoBox.exe", "msedgewebview2.exe"},
		},
		{
			name:         "existing has messy pipes and whitespace",
			existing:     "  javaw.exe | |Photoshop.exe| ",
			targets:      []string{"NeoBox.exe"},
			wantModified: true,
			wantContains: []string{"javaw.exe", "Photoshop.exe", "NeoBox.exe"},
		},
		{
			name:         "partial presence: one target exists, one is missing",
			existing:     "neobox.exe|Photoshop.exe",
			targets:      []string{"NeoBox.exe", "msedgewebview2.exe"},
			wantModified: true,
			wantContains: []string{"neobox.exe", "Photoshop.exe", "msedgewebview2.exe"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, modified := MergeExclusions(tc.existing, tc.targets)
			if modified != tc.wantModified {
				t.Errorf("MergeExclusions(%q, %v) modified = %v, want %v", tc.existing, tc.targets, modified, tc.wantModified)
			}
			parts := strings.Split(got, "|")
			for _, want := range tc.wantContains {
				found := false
				for _, part := range parts {
					if strings.EqualFold(strings.TrimSpace(part), want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("MergeExclusions(%q, %v) = %q; missing expected entry %q", tc.existing, tc.targets, got, want)
				}
			}
		})
	}
}

func TestRemoveFromInclude(t *testing.T) {
	tests := []struct {
		name         string
		existing     string
		targets      []string
		wantModified bool
		wantResult   string
	}{
		{
			name:         "empty existing string",
			existing:     "",
			targets:      []string{"NeoBox.exe"},
			wantModified: false,
			wantResult:   "",
		},
		{
			name:         "include contains target and other app",
			existing:     "notepad.exe|NeoBox.exe|calc.exe",
			targets:      []string{"neobox.exe"},
			wantModified: true,
			wantResult:   "notepad.exe|calc.exe",
		},
		{
			name:         "include contains only target",
			existing:     "neobox.exe",
			targets:      []string{"NeoBox.exe"},
			wantModified: true,
			wantResult:   "",
		},
		{
			name:         "include contains no targets",
			existing:     "notepad.exe|calc.exe",
			targets:      []string{"NeoBox.exe"},
			wantModified: false,
			wantResult:   "notepad.exe|calc.exe",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, modified := RemoveFromInclude(tc.existing, tc.targets)
			if modified != tc.wantModified {
				t.Errorf("RemoveFromInclude(%q, %v) modified = %v, want %v", tc.existing, tc.targets, modified, tc.wantModified)
			}
			if got != tc.wantResult {
				t.Errorf("RemoveFromInclude(%q, %v) = %q, want %q", tc.existing, tc.targets, got, tc.wantResult)
			}
		})
	}
}

func TestGetProtectedProcesses(t *testing.T) {
	procs := GetProtectedProcesses()
	expected := []string{"NeoBox.exe", "msedgewebview2.exe", "NeoBox_Setup.exe"}
	for _, exp := range expected {
		found := false
		for _, p := range procs {
			if strings.EqualFold(p, exp) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("GetProtectedProcesses() missing %q, got: %v", exp, procs)
		}
	}
}

func TestEnsureWindhawkCompatibility(t *testing.T) {
	// Should execute gracefully without panic
	err := EnsureWindhawkCompatibility()
	if err != nil {
		t.Logf("EnsureWindhawkCompatibility returned error (expected if non-elevated): %v", err)
	}
}

package service

import "testing"

func TestValidateUpdateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"github release page", "https://github.com/Dvarais/NeoBox/releases/latest", false},
		{"release asset host", "https://objects.githubusercontent.com/g/NeoBox_Setup.exe", false},
		{"api subdomain", "https://api.github.com/repos/Dvarais/NeoBox", false},
		{"raw content host", "https://raw.githubusercontent.com/x/y.srs", false},

		{"plain http is rejected", "http://github.com/Dvarais/NeoBox/setup.exe", true},
		{"unrelated host", "https://evil.com/NeoBox_Setup.exe", true},
		// The suffix check must not be fooled by a host that merely contains or
		// is prefixed with the trusted domain.
		{"trusted domain as prefix", "https://github.com.evil.com/setup.exe", true},
		{"trusted domain as substring", "https://notgithub.com/setup.exe", true},
		{"hyphenated lookalike", "https://evil-github.com/setup.exe", true},
		{"userinfo trick", "https://github.com@evil.com/setup.exe", true},
		{"empty url", "", true},
		{"scheme-less", "github.com/setup.exe", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUpdateURL(tc.url)
			if tc.wantErr && err == nil {
				t.Errorf("validateUpdateURL(%q) accepted an untrusted URL", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateUpdateURL(%q) rejected a trusted URL: %v", tc.url, err)
			}
		})
	}
}

// fetchAssetSignature must refuse anything that is not a usable signature, since
// its failure is what suppresses the in-app install offer.
func TestFetchAssetSignatureRequiresMatchingAsset(t *testing.T) {
	s := &AppService{}

	assets := []interface{}{
		map[string]interface{}{
			"name":                 "NeoBox_Setup_v1.7.0.exe",
			"browser_download_url": "https://github.com/Dvarais/NeoBox/releases/download/v1.7.0/NeoBox_Setup_v1.7.0.exe",
		},
	}

	if _, err := s.fetchAssetSignature(assets, "NeoBox_Setup_v1.7.0.exe"); err == nil {
		t.Error("expected an error when the .sig asset is absent")
	}

	// A .sig asset hosted off GitHub must be rejected before it is fetched.
	assets = append(assets, map[string]interface{}{
		"name":                 "NeoBox_Setup_v1.7.0.exe.sig",
		"browser_download_url": "https://evil.com/NeoBox_Setup_v1.7.0.exe.sig",
	})
	if _, err := s.fetchAssetSignature(assets, "NeoBox_Setup_v1.7.0.exe"); err == nil {
		t.Error("expected an error for a signature hosted on an untrusted domain")
	}
}

func TestIsNewer(t *testing.T) {
	s := &AppService{}
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"1.7.1", "1.7.0", true},
		{"1.8.0", "1.7.0", true},
		{"2.0.0", "1.7.0", true},
		{"1.7.0", "1.7.0", false},
		{"1.6.9", "1.7.0", false},
		{"1.7.0.1", "1.7.0", true},
		{"1.10.0", "1.9.0", true}, // numeric, not lexicographic
		{"1.9.0", "1.10.0", false},

		// A leading "v" must be tolerated on either side.
		{"v1.7.1", "1.7.0", true},
		{"v1.7.0", "v1.7.0", false},

		// Pre-release suffixes used to make strconv.Atoi fail and yield 0, so
		// 1.7.1-beta compared as 1.7.0 — i.e. OLDER than the release it follows.
		{"1.7.1-beta", "1.7.0", true},
		{"1.7.1-beta.2", "1.7.0", true},
		{"1.8.0-rc1", "1.7.9", true},
		{"1.7.0-beta", "1.7.0", false},
	}
	for _, tc := range tests {
		if got := s.isNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want []int
	}{
		{"1.7.0", []int{1, 7, 0}},
		{"v1.7.0", []int{1, 7, 0}},
		{" v1.7.0 ", []int{1, 7, 0}},
		{"1.7.1-beta.2", []int{1, 7, 1}},
		{"1.7", []int{1, 7}},
		{"2", []int{2}},
		// Nothing numeric to compare — must yield no components rather than
		// a misleading [0].
		{"", nil},
		{"beta", nil},
		// Regression: Replace(tag, "v", "", 1) turned this into "ersion-2".
		{"version-2", nil},
	}

	for _, tc := range tests {
		got := parseVersion(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("parseVersion(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseVersion(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

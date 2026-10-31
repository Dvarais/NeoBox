package core

import (
	"reflect"
	"testing"
)

func TestParseSubscriptionUserInfo(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected *SubscriptionUserInfo
	}{
		{
			name:   "standard full header",
			header: "upload=1073741824; download=10737418240; total=107374182400; expire=1735689600",
			expected: &SubscriptionUserInfo{
				Upload:   1073741824,
				Download: 10737418240,
				Total:    107374182400,
				Expire:   1735689600,
			},
		},
		{
			name:   "unlimited quota with no expire",
			header: "upload=52428800; download=104857600; total=0; expire=0",
			expected: &SubscriptionUserInfo{
				Upload:   52428800,
				Download: 104857600,
				Total:    0,
				Expire:   0,
			},
		},
		{
			name:   "irregular spacing and mixed casing",
			header: "  Upload = 2048 ;  DoWnLoAd = 4096 ; TOTAL= 8192 ; Expire = 1800000000  ",
			expected: &SubscriptionUserInfo{
				Upload:   2048,
				Download: 4096,
				Total:    8192,
				Expire:   1800000000,
			},
		},
		{
			name:   "partial fields only upload and download",
			header: "upload=1000; download=2000",
			expected: &SubscriptionUserInfo{
				Upload:   1000,
				Download: 2000,
				Total:    0,
				Expire:   0,
			},
		},
		{
			name:     "empty header",
			header:   "",
			expected: nil,
		},
		{
			name:     "invalid gibberish",
			header:   "invalid-header-content; not-kv",
			expected: nil,
		},
		{
			name:     "non-numeric values",
			header:   "upload=abc; download=xyz",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := ParseSubscriptionUserInfo(tc.header)
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("ParseSubscriptionUserInfo(%q) = %+v, want %+v", tc.header, actual, tc.expected)
			}
		})
	}
}

package updater

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest   string
		current  string
		expected bool
	}{
		{"v0.9.1", "0.9.0-build_20260928-2223", true},
		{"0.9.1", "0.9.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.9.0", "0.9.0-build_20260928-2223", false},
		{"v0.9.0", "v0.9.0", false},
		{"v0.8.5", "v0.9.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v0.10.0", "v0.9.9", true},
		{"v0.9.2", "0.9.1", true},
	}

	for _, tt := range tests {
		got := IsNewer(tt.latest, tt.current)
		if got != tt.expected {
			t.Errorf("IsNewer(%q, %q) = %v; want %v", tt.latest, tt.current, got, tt.expected)
		}
	}
}

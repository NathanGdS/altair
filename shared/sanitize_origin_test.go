package shared

import "testing"

func TestSanitizeOrigin(t *testing.T) {
	valid := []string{"orders", "orders-2026", "orders_2026", "A1", "stress-test"}
	for _, origin := range valid {
		got, err := SanitizeOrigin(origin)
		if err != nil {
			t.Errorf("SanitizeOrigin(%q) returned unexpected error: %v", origin, err)
		}
		if got != origin {
			t.Errorf("SanitizeOrigin(%q) = %q, want unchanged", origin, got)
		}
	}

	// Each of these is a path-traversal or filesystem-path-injection attempt (or otherwise
	// outside the allowed charset). SanitizeOrigin must reject all of them so that
	// pendingFilePath/failedFilePath (workers/delivery.go) never receive them.
	invalid := []string{
		"",
		"../../evil",
		"../evil",
		"a/../../b",
		"/etc/passwd",
		`..\..\evil`,
		"orders/../../etc",
		"orders with spaces",
		"orders.json",
		"orders;rm -rf",
	}
	for _, origin := range invalid {
		if _, err := SanitizeOrigin(origin); err == nil {
			t.Errorf("SanitizeOrigin(%q) = nil error, want rejection", origin)
		}
	}
}

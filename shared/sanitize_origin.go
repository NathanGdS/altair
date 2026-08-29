package shared

import (
	"fmt"
	"regexp"
)

// originPattern is the allowed charset for an origin: letters, digits, underscore, and
// hyphen. Origin is interpolated directly into filesystem paths (see
// workers/delivery.go's pendingFilePath/failedFilePath), so anything outside this charset
// — most importantly path separators and "." — is rejected rather than silently stripped,
// since stripping could let two distinct, attacker-chosen origins collapse onto the same
// sanitized value and cross-deliver messages between them.
var originPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SanitizeOrigin validates that origin is safe to interpolate into a filesystem path.
// It is applied at every untrusted entry point that produces an origin — POST /publish
// and POST /consumers — so that by the time an origin reaches a filesystem-path sink
// (workers/delivery.go's pendingFilePath/failedFilePath) it is guaranteed to already be
// restricted to [A-Za-z0-9_-]. This closes the path-traversal class of bug (e.g.
// origin="../../evil") for those two new sinks.
func SanitizeOrigin(origin string) (string, error) {
	if !originPattern.MatchString(origin) {
		return "", fmt.Errorf("invalid origin %q: must be non-empty and contain only letters, digits, underscores, or hyphens", origin)
	}
	return origin, nil
}

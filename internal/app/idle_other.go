//go:build !windows

package app

import "time"

// SystemIdleDuration is a stub returning 0 on non-Windows platforms.
func SystemIdleDuration() time.Duration {
	return 0
}

// Package autostart makes gate4ai-sync start with the user's session. Each
// OS gets its own file (build-tagged by filename) since the mechanism is
// entirely different — a bare text/registry write in every case, no CGO
// and no platform SDK.
package autostart

// Enable registers the current executable to start automatically. Calling
// it again (e.g. on every launch) must be a no-op if it is already
// registered — see each platform file's Enable for how it achieves that.
func Enable() error { return enable() }

// Disable removes the registration, if any.
func Disable() error { return disable() }

// IsEnabled reports whether the registration currently exists.
func IsEnabled() (bool, error) { return isEnabled() }

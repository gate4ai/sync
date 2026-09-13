// Package browser opens a URL in the system's default browser — the one
// native-feeling thing this client's tray menu does, since it has no GUI of
// its own (see internal/webui).
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches url in the default browser. No CGO, no platform SDK — one
// command per OS, exactly what the shell's own "open a link" does.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// rundll32 rather than "cmd /c start" — start treats "&", "^" and
		// quotes in the URL as shell syntax, and a client_id is a UUID
		// today but nothing stops a future URL from including a query
		// string that breaks that.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux and other Unix desktops
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

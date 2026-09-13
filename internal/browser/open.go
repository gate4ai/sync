// Package browser opens a URL in the system's default browser — the one
// native-feeling thing this client's tray menu does, since it has no GUI of
// its own (see internal/webui).
package browser

import (
	"context"
	"os/exec"
	"runtime"
)

// Open launches url in the default browser. No CGO, no platform SDK — one
// command per OS, exactly what the shell's own "open a link" does.
//
// It takes no context because there is nothing to cancel: cmd.Start()
// forks the browser and returns immediately, and the child outliving this
// process is the point, not something a deadline should ever cut off. A
// background context (no possible deadline or cancellation) is passed to
// CommandContext purely to keep the linter's "always use *Context" rule
// happy without pretending a real cancellation path exists here.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(context.Background(), "open", url)
	case "windows":
		// rundll32 rather than "cmd /c start" — start treats "&", "^" and
		// quotes in the URL as shell syntax, and a client_id is a UUID
		// today but nothing stops a future URL from including a query
		// string that breaks that.
		cmd = exec.CommandContext(context.Background(), "rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux and other Unix desktops
		cmd = exec.CommandContext(context.Background(), "xdg-open", url)
	}
	return cmd.Start()
}

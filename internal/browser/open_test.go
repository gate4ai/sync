package browser_test

import (
	"testing"

	"github.com/gate4ai/sync/internal/browser"
)

// Open shells out to a real OS command (xdg-open/open/rundll32), so there
// is nothing to assert against without actually opening a browser. This
// just confirms it does not panic and returns some result for a URL —
// exec.Command's Start() only fails if the binary itself cannot be found
// or launched, which on a CI box without a desktop is expected and fine to
// ignore here.
func TestOpenDoesNotPanic(t *testing.T) {
	_ = browser.Open("http://127.0.0.1:1/")
}

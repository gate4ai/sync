package instance_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gate4ai/sync/internal/instance"
)

// serve stands in for a client's settings page answering GET /instance.
func serve(t *testing.T, token string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != instance.Path {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(token))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

func TestLookupFindsTheRecordingClient(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ui-url")
	token := instance.NewToken()
	settingsURL := serve(t, token)
	if _, err := instance.Record(file, settingsURL, token); err != nil {
		t.Fatal(err)
	}
	got, err := instance.Lookup(t.Context(), file)
	if err != nil || got != settingsURL {
		t.Fatalf("Lookup = %q, %v; want %q", got, err, settingsURL)
	}
}

func TestLookupWithoutAFile(t *testing.T) {
	_, err := instance.Lookup(t.Context(), filepath.Join(t.TempDir(), "ui-url"))
	if !errors.Is(err, instance.ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

// A crash leaves the file behind, pointing at a port nothing listens on.
func TestLookupWithAStaleFile(t *testing.T) {
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	file := filepath.Join(t.TempDir(), "ui-url")
	if _, err := instance.Record(file, "http://"+addr+"/", instance.NewToken()); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Lookup(t.Context(), file); !errors.Is(err, instance.ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

// After a crash the port can be taken by something else — another program,
// or another user's client. Its address must not be handed out as ours.
func TestLookupRejectsAnotherProgramOnThePort(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ui-url")
	settingsURL := serve(t, instance.NewToken())
	if _, err := instance.Record(file, settingsURL, instance.NewToken()); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Lookup(t.Context(), file); !errors.Is(err, instance.ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

// A file from before tokens existed, or cut short, holds no token to check.
func TestLookupRejectsAFileWithoutAToken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ui-url")
	settingsURL := serve(t, "")
	if err := os.WriteFile(file, []byte(settingsURL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Lookup(t.Context(), file); !errors.Is(err, instance.ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestRemoveDeletesOwnRecordOnly(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ui-url")
	removeFirst, err := instance.Record(file, "http://127.0.0.1:1/", "first")
	if err != nil {
		t.Fatal(err)
	}
	removeSecond, err := instance.Record(file, "http://127.0.0.1:2/", "second")
	if err != nil {
		t.Fatal(err)
	}

	removeFirst() // the first client quits after the second has started
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), "second") {
		t.Fatalf("second client's record was removed by the first: %q, %v", data, err)
	}

	removeSecond()
	removeSecond() // safe to call twice
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("record still there after its own client quit: %v", err)
	}
}

func TestRecordFileIsPrivate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "ui-url")
	if _, err := instance.Record(file, "http://127.0.0.1:1/", "t"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("mode = %v, want readable by its owner only", perm)
	}
	if _, err := os.Stat(file + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}
}

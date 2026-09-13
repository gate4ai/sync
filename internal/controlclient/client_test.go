package controlclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gate4ai/sync/internal/controlclient"
)

// fakeServer emulates just enough of internal/syncapi (gate4ai/server) to
// exercise the client against the contract in docs/sync-api.md.
type fakeServer struct {
	linked   bool
	mounts   map[string]string // folder_id -> slug
	settings controlclient.Settings
}

func newFakeServer(t *testing.T) (*httptest.Server, *fakeServer) {
	t.Helper()
	fs := &fakeServer{
		mounts: map[string]string{},
		settings: controlclient.Settings{
			MaxFileSizeBytes: 1 << 20, PollIntervalSeconds: 60, VaultSlug: "home-pc",
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(srv.Close)
	return srv, fs
}

func (fs *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/sync/v1/settings" && r.Method == http.MethodGet:
		if r.Header.Get("X-Gate4AI-Client-ID") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(fs.settings)

	case r.URL.Path == "/api/sync/v1/mounts" && r.Method == http.MethodPost:
		var req struct{ FolderID, FolderName string }
		req.FolderID = "folder-1"
		_ = json.NewDecoder(r.Body).Decode(&req)
		slug, exists := fs.mounts["folder-1"]
		if !exists {
			slug = "documents"
			fs.mounts["folder-1"] = slug
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(controlclient.RegisteredMount{Slug: slug, Username: "v-1", Secret: "sekret"})
			return
		}
		_ = json.NewEncoder(w).Encode(controlclient.RegisteredMount{Slug: slug})

	case r.URL.Path == "/api/sync/v1/mounts" && r.Method == http.MethodGet:
		var mounts []controlclient.Mount
		for id, slug := range fs.mounts {
			mounts = append(mounts, controlclient.Mount{FolderID: id, Slug: slug})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"mounts": mounts})

	case r.Method == http.MethodDelete && len(r.URL.Path) > len("/api/sync/v1/mounts/"):
		id := r.URL.Path[len("/api/sync/v1/mounts/"):]
		delete(fs.mounts, id)
		w.WriteHeader(http.StatusNoContent)

	case len(r.URL.Path) > len("/api/sync/v1/pairing/") && r.Method == http.MethodGet:
		status := "pending"
		if fs.linked {
			status = "linked"
		}
		_ = json.NewEncoder(w).Encode(controlclient.PairingStatus{Status: status, VaultSlug: "home-pc"})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestSettingsSendsTheClientIDHeader(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := &controlclient.Client{BaseURL: srv.URL, ClientID: "c1"}

	s, err := c.Settings(t.Context())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if s.VaultSlug != "home-pc" {
		t.Errorf("VaultSlug = %q, want %q", s.VaultSlug, "home-pc")
	}
}

func TestSettingsFailsWithoutAClientID(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := &controlclient.Client{BaseURL: srv.URL}
	if _, err := c.Settings(t.Context()); err == nil {
		t.Fatal("Settings without a client_id succeeded, want an error")
	}
}

func TestRegisterMountIsIdempotentAndSecretIsNotRepeated(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := &controlclient.Client{BaseURL: srv.URL, ClientID: "c1"}

	first, err := c.RegisterMount(t.Context(), "folder-1", "Documents")
	if err != nil {
		t.Fatalf("RegisterMount: %v", err)
	}
	if first.Secret == "" {
		t.Error("expected a secret on the first registration")
	}

	second, err := c.RegisterMount(t.Context(), "folder-1", "Documents")
	if err != nil {
		t.Fatalf("RegisterMount again: %v", err)
	}
	if second.Secret != "" {
		t.Error("re-registration must not repeat the secret")
	}
	if second.Slug != first.Slug {
		t.Errorf("Slug changed across calls: %q != %q", second.Slug, first.Slug)
	}
}

func TestPairingStatusReflectsLinking(t *testing.T) {
	srv, fs := newFakeServer(t)
	c := &controlclient.Client{BaseURL: srv.URL, ClientID: "c1"}

	pending, err := c.PairingStatus(t.Context())
	if err != nil {
		t.Fatalf("PairingStatus: %v", err)
	}
	if pending.Status != "pending" {
		t.Fatalf("Status = %q, want pending", pending.Status)
	}

	fs.linked = true
	linked, err := c.PairingStatus(t.Context())
	if err != nil {
		t.Fatalf("PairingStatus: %v", err)
	}
	if linked.Status != "linked" {
		t.Fatalf("Status = %q, want linked", linked.Status)
	}
}

package webui

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
)

type folderRow struct {
	ID, Path, Status string
	// Slug, FilesURL and SettingsURL are set once the folder's mount is
	// registered: the slug in its status line links to the mount's files in
	// the cabinet, and a gear next to Remove opens that mount's settings —
	// the same pair of links the cabinet's own vault list gives each mount.
	// Settings live per mount on the server, so the link sits on the folder
	// rather than on the page-wide "Server settings" block.
	Slug, FilesURL, SettingsURL string
	// CanRemove is false for a registered folder while the client is not
	// linked: removing it has to disable its mount on the server first,
	// which needs a working connection.
	CanRemove bool
}

type homePage struct {
	Linked        bool
	VaultSlug     string
	Status        string // "Synced 2 minutes ago" / "Error: ..." / "" before linking
	StatusIsError bool
	// Reconnect is set when the client isn't linked but already has folders
	// configured: the person connected before and only needs to pair again.
	// Without folders it is a first run, and adding a folder starts pairing.
	Reconnect  bool
	Folders    []folderRow
	Settings   []settingRow
	CabinetURL string
	ClientID   string
	HomeURL    string
}

// settingRow is one line of the "Server settings" block — see its own
// comment on why this is a list of rows rather than one packed sentence.
type settingRow struct {
	Name, Value string
}

// statusLine turns the loop's last-known outcome into the one sentence the
// home page shows under "Connected to vault ...". An error takes priority
// over the last success — a stale "synced 5 minutes ago" next to a fresh
// failure would read as everything being fine.
func statusLine(now time.Time, lastSync time.Time, errText string, erroredAt time.Time) string {
	if errText != "" {
		return fmt.Sprintf("Error %s: %s", relativeTime(now, erroredAt), errText)
	}
	if lastSync.IsZero() {
		return "Not synced yet"
	}
	return "Synced " + relativeTime(now, lastSync)
}

func relativeTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return t.Format("2006-01-02 15:04")
	}
}

// folderRows builds the "Folders" list rows shared by the home page and the
// browse page — see browse's own comment on why it needs this list too, not
// just an inline marker on whichever entry happens to match.
func folderRows(cfg config.Config, folders []config.Folder) []folderRow {
	var rows []folderRow
	for _, f := range folders {
		row := folderRow{ID: f.ID, Path: f.Path, Status: "waiting to connect", CanRemove: true}
		switch {
		case f.Registered():
			slug := url.PathEscape(f.Slug)
			row.Status = "synced as " + f.Slug
			row.Slug = f.Slug
			row.CanRemove = cfg.Linked
			row.FilesURL = cfg.EffectiveCabinetURL() + "/files/" + slug
			row.SettingsURL = cfg.EffectiveCabinetURL() + "/settings/" + slug
		case cfg.Linked:
			row.Status = "registering…"
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	cfg := *s.Config
	folders := append([]config.Folder(nil), s.Config.Folders...)
	s.Mu.Unlock()

	page := homePage{
		Linked:     cfg.Linked,
		VaultSlug:  cfg.VaultSlug,
		CabinetURL: cfg.EffectiveCabinetURL(),
		ClientID:   cfg.ClientID,
		HomeURL:    cfg.EffectiveCabinetURL(),
		Folders:    folderRows(cfg, folders),
	}
	if cfg.Linked {
		snap := s.Status.Snapshot()
		page.Status = statusLine(time.Now(), snap.LastSync, snap.Error, snap.ErroredAt)
		page.StatusIsError = snap.Error != ""
	}

	if cfg.Linked {
		if settings, err := s.Control().Settings(r.Context()); err == nil {
			page.Settings = []settingRow{
				{Name: "Allowed types", Value: joinOrAll(settings.AllowedExtensions)},
				{Name: "Max file size", Value: humanSize(settings.MaxFileSizeBytes)},
				{Name: "Poll interval", Value: humanInterval(settings.PollIntervalSeconds)},
			}
		} else if errors.Is(err, controlclient.ErrUnauthorized) {
			// The server no longer accepts this client (pairing revoked) —
			// same reset the sync loop does on this error, so the next
			// render falls into the "not connected" branch below and offers
			// the same Connect button first-time pairing uses.
			s.Log.Warn("server no longer accepts this client; re-pairing", "err", err)
			s.Mu.Lock()
			s.Config.Unlink()
			saveErr := s.SaveConfig()
			s.Mu.Unlock()
			if saveErr != nil {
				s.Log.Error("save config after unlinking", "err", saveErr)
			}
			page.Linked = false
		} else {
			s.Log.Warn("read settings for home page", "err", err)
		}
	}

	page.Reconnect = !page.Linked && len(folders) > 0

	s.render(w, "home", page)
}

func joinOrAll(exts []string) string {
	if exts == nil {
		return "all types"
	}
	return strings.Join(exts, ", ")
}

func humanSize(n int64) string {
	if n <= 0 {
		return "no limit"
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1024*1024))
}

func humanInterval(seconds int) string {
	if seconds%60 == 0 {
		return fmt.Sprintf("%d min", seconds/60)
	}
	return fmt.Sprintf("%d s", seconds)
}

// browse is the HTML file picker: os.ReadDir, one level, with breadcrumbs.
// No native dialogs — see the package comment. Each listed subfolder gets
// its own Sync button rather than the page offering one action for
// whichever folder happens to be open — a folder worth syncing is usually
// a child of the one you're browsing, not the browsing point itself, and a
// button per row skips the extra navigate-in-then-confirm step.
type browseEntry struct {
	Name, Path string
	// Synced is true when this exact path is already a configured folder —
	// it keeps showing up in the listing rather than being hidden, just
	// with its status in place of a Sync button, so browsing never looks
	// like it silently dropped something already set up.
	Synced bool
}

type browsePage struct {
	Path    string
	Parent  string
	Entries []browseEntry
	// Folders is the same "already configured" list the home page shows —
	// kept visible here too, in full (path, status, Remove), not just the
	// inline "Already syncing" marker on a matching entry below: a folder
	// you added five levels up would otherwise vanish from view the moment
	// you're browsing anywhere else.
	Folders []folderRow
	HomeURL string
}

func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/"
		}
		dir = home
	}
	dir = filepath.Clean(dir)

	s.Mu.Lock()
	cfg := *s.Config
	folders := append([]config.Folder(nil), s.Config.Folders...)
	s.Mu.Unlock()
	synced := make(map[string]bool, len(folders))
	for _, f := range folders {
		synced[f.Path] = true
	}
	folderList := folderRows(cfg, folders)

	entries, err := os.ReadDir(dir)
	if err != nil {
		s.render(w, "browse", browsePage{Path: dir, Parent: filepath.Dir(dir), Folders: folderList, HomeURL: cfg.EffectiveCabinetURL()})
		return
	}
	var rows []browseEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		rows = append(rows, browseEntry{Name: e.Name(), Path: path, Synced: synced[path]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	s.render(w, "browse", browsePage{Path: dir, Parent: parent, Entries: rows, Folders: folderList, HomeURL: cfg.EffectiveCabinetURL()})
}

func (s *Server) addFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	path := filepath.Clean(r.FormValue("path"))
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		http.Error(w, "not a directory", http.StatusBadRequest)
		return
	}

	s.Mu.Lock()
	exists := false
	for _, f := range s.Config.Folders {
		if f.Path == path {
			exists = true
			break
		}
	}
	if !exists {
		s.Config.Folders = append(s.Config.Folders, config.Folder{ID: config.NewID(), Path: path})
		err = s.SaveConfig()
	}
	s.Mu.Unlock()

	if err != nil {
		s.Log.Error("save config after adding folder", "err", err)
	}
	s.redirectHomeOrLink(w, r)
}

// removeFolder disables the folder's mount on the server first — issue #38:
// disconnecting a folder deletes its files from the vault — and only then
// drops it locally. If disabling fails, the folder stays configured rather
// than leaving an orphaned mount the user has no way back to.
func (s *Server) removeFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")

	s.Mu.Lock()
	folder, ok := s.Config.Folder(id)
	s.Mu.Unlock()
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if folder.Registered() {
		if err := s.Control().DisableMount(r.Context(), id); err != nil {
			s.Log.Error("disable mount", "folder_id", id, "err", err)
			s.renderError(w, http.StatusBadGateway, "Could not remove the folder",
				"gate4.ai did not confirm disconnecting this folder, so it is still configured. Check your connection and try again.")
			return
		}
	}

	s.Mu.Lock()
	s.Config.RemoveFolder(id)
	err := s.SaveConfig()
	s.Mu.Unlock()
	if err != nil {
		s.Log.Error("save config after removing folder", "err", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// redirectHomeOrLink ends a request that changed the folder list: back to
// the home page once this installation is linked, straight on to pairing
// otherwise. Pressing Sync on a folder is already the user saying "sync
// this with gate4.ai", so an unlinked client does not stop on the home page
// asking for a second Connect click.
func (s *Server) redirectHomeOrLink(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	linked := s.Config.Linked
	dest := s.linkURL()
	s.Mu.Unlock()

	if linked {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// linkURL is the cabinet's /link page for this installation, with a return
// URL pointing back at this loopback server so /link can send the browser
// home once pairing finishes instead of leaving the user stranded on the
// cabinet. The caller holds s.Mu.
func (s *Server) linkURL() string {
	returnURL := "http://" + s.Addr() + "/"
	return s.Config.EffectiveCabinetURL() + "/link?client=" + url.QueryEscape(s.Config.ClientID) + "&return=" + url.QueryEscape(returnURL)
}

// save is the Connect button: it starts pairing by sending the browser —
// the very one showing this settings page — to /link. Adding a folder does
// the same on its own (see redirectHomeOrLink); this button stays for the
// case the user came back without finishing pairing. There is nothing else
// to persist here; folders are already saved as they are added.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	s.redirectHomeOrLink(w, r)
}

type errorPage struct {
	Title, Message string
	HomeURL        string
}

// renderError shows a failure as a regular page of the client instead of a
// bare plain-text body, with a way back to the home page.
func (s *Server) renderError(w http.ResponseWriter, status int, title, message string) {
	s.Mu.Lock()
	home := s.Config.EffectiveCabinetURL()
	s.Mu.Unlock()
	w.WriteHeader(status)
	s.render(w, "error", errorPage{Title: title, Message: message, HomeURL: home})
}

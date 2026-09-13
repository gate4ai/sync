package webui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gate4ai/sync/internal/config"
)

type folderRow struct {
	ID, Path, Status string
}

type homePage struct {
	Linked     bool
	VaultSlug  string
	Folders    []folderRow
	Settings   *settingsView
	CabinetURL string
	ClientID   string
}

type settingsView struct {
	AllowedExtensions string
	MaxFileSize       string
	PollInterval      string
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
	}
	for _, f := range folders {
		status := "waiting to connect"
		switch {
		case f.Registered():
			status = "synced as " + f.Slug
		case cfg.Linked:
			status = "registering…"
		}
		page.Folders = append(page.Folders, folderRow{ID: f.ID, Path: f.Path, Status: status})
	}

	if cfg.Linked {
		if settings, err := s.Control().Settings(r.Context()); err == nil {
			page.Settings = &settingsView{
				AllowedExtensions: joinOrAll(settings.AllowedExtensions),
				MaxFileSize:       humanSize(settings.MaxFileSizeBytes),
				PollInterval:      humanInterval(settings.PollIntervalSeconds),
			}
		} else {
			s.Log.Warn("read settings for home page", "err", err)
		}
	}

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
// No native dialogs — see the package comment.
type browseEntry struct {
	Name, Path string
}

type browsePage struct {
	Path     string
	Parent   string
	Entries  []browseEntry
	FolderID string // set when browsing to replace an existing folder's path — unused in MVP, reserved
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

	entries, err := os.ReadDir(dir)
	if err != nil {
		s.render(w, "browse", browsePage{Path: dir, Parent: filepath.Dir(dir)})
		return
	}
	var rows []browseEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		rows = append(rows, browseEntry{Name: e.Name(), Path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	s.render(w, "browse", browsePage{Path: dir, Parent: parent, Entries: rows})
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
	for _, f := range s.Config.Folders {
		if f.Path == path {
			s.Mu.Unlock()
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	s.Config.Folders = append(s.Config.Folders, config.Folder{ID: config.NewID(), Path: path})
	err = s.SaveConfig()
	s.Mu.Unlock()

	if err != nil {
		s.Log.Error("save config after adding folder", "err", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
			http.Error(w, "could not disconnect this folder from the server; try again", http.StatusBadGateway)
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

// save is the button that starts pairing: it sends the browser — the very
// one showing this settings page — to /link. There is nothing else to
// persist here; folders are already saved as they are added.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	linked := s.Config.Linked
	cabinet := s.Config.EffectiveCabinetURL()
	clientID := s.Config.ClientID
	s.Mu.Unlock()

	if linked {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, cabinet+"/link?client="+clientID, http.StatusSeeOther)
}

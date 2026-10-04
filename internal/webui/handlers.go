package webui

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/localpath"
)

type folderRow struct {
	ID, Path, Status string
	// Slug, FilesURL and SettingsURL are set once the folder's mount is
	// registered: the slug in its status line links to the mount's files in
	// the cabinet, and a gear next to Remove opens that mount's settings —
	// the same pair of links the cabinet's own vault list gives each mount.
	// Settings live per mount on the server, so the link sits on the folder.
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
	CabinetURL string
	ClientID   string
	HomeURL    string
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

	page.Reconnect = !page.Linked && len(folders) > 0

	s.render(w, "home", page)
}

// browseEntry is one subfolder in the listing.
type browseEntry struct {
	Name, Path string
	// Synced is true when this exact path is already a configured folder —
	// it keeps showing up in the listing rather than being hidden, just
	// with its status in place of a Sync button, so browsing never looks
	// like it silently dropped something already set up.
	Synced bool
}

// pickerPage is the folder chooser in both its shapes: the roots screen —
// drives and the folders most people mean — when nothing is open, and one
// folder's listing otherwise. One page rather than two, because the path box,
// the notice above the listing and the list of folders already configured
// belong on both.
//
// There are no native dialogs here; see the package comment. What there is
// instead has to cover the two things a native dialog would have given for
// free: reaching a second drive at all, and pasting a path.
type pickerPage struct {
	// Dir is the folder being listed, empty on the roots screen.
	Dir     string
	DirName string
	Crumbs  []localpath.Crumb
	Entries []browseEntry
	// AlreadySynced is Dir's own state: its main button becomes a badge
	// rather than offering to add the same folder a second time.
	AlreadySynced bool

	// Input is what the path box holds — the path that was asked for, kept
	// even when it turned out not to exist, so a typo is corrected in place
	// instead of hunted down and pasted again.
	Input       string
	Placeholder string
	PasteHint   string
	// Problem is the one sentence shown when the folder on screen is not the
	// one that was asked for: a file's path, a typo, or no access.
	Problem        string
	ProblemIsError bool

	QuickAccess    []localpath.Place
	Volumes        []localpath.Place
	VolumesHeading string

	// Folders is the same "already configured" list the home page shows —
	// kept visible here too, in full (path, status, Remove), not just as an
	// inline marker on a matching entry below: a folder added five levels up
	// would otherwise vanish from view the moment you browse anywhere else.
	Folders []folderRow
	HomeURL string
}

// browse serves the picker. An empty path is the roots screen rather than the
// home folder: opening in the home folder is what made a folder on D:
// unreachable, since walking up from there stops at the top of C:.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	target := localpath.Resolve(r.URL.Query().Get("path"))

	s.Mu.Lock()
	cfg := *s.Config
	folders := append([]config.Folder(nil), s.Config.Folders...)
	s.Mu.Unlock()

	page := pickerPage{
		Dir:            target.Dir,
		Input:          target.Asked,
		Placeholder:    pathPlaceholder(),
		PasteHint:      pasteHint(),
		VolumesHeading: volumesHeading(),
		Folders:        folderRows(cfg, folders),
		HomeURL:        cfg.EffectiveCabinetURL(),
	}
	page.Problem, page.ProblemIsError = problemLine(target)

	if target.Dir == "" {
		page.QuickAccess, page.Volumes = localpath.QuickAccess(), localpath.Volumes()
		s.render(w, "picker", page)
		return
	}

	if page.Input == "" {
		page.Input = target.Dir
	}
	configured := folderPaths(folders)
	page.DirName = folderName(target.Dir)
	page.Crumbs = localpath.Crumbs(target.Dir)
	page.AlreadySynced = containsFolder(configured, target.Dir)
	page.Entries = subfolders(target.Dir, configured)
	s.render(w, "picker", page)
}

// subfolders lists what can be browsed into from dir. Plain files are left
// out: the picker chooses a folder, and a listing of every document in it
// would bury the folders among them.
func subfolders(dir string, configured []string) []browseEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var rows []browseEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		rows = append(rows, browseEntry{Name: e.Name(), Path: path, Synced: containsFolder(configured, path)})
	}
	// Case-insensitively, the way a file manager orders the same list: "apps"
	// belongs next to "Archive", not after every capitalised name.
	sort.Slice(rows, func(i, j int) bool {
		a, b := strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)
		if a == b {
			return rows[i].Name < rows[j].Name
		}
		return a < b
	})
	return rows
}

// problemLine is the sentence above the listing when the folder shown is not
// the one that was asked for. Pasting a file's path, or mistyping one, is
// normal enough that neither should be a dead end: the picker opens the
// closest folder it could and says why it is there.
func problemLine(t localpath.Target) (text string, isError bool) {
	switch t.Outcome {
	case localpath.WasFile:
		return t.Asked + " is a file — showing the folder it is in.", false
	case localpath.Missing:
		if t.Dir == "" {
			return "There is no folder at " + t.Asked + ".", true
		}
		return "There is no folder at " + t.Asked + " — showing the closest one above it.", true
	case localpath.Denied:
		if t.Dir == "" {
			return t.Asked + " could not be opened.", true
		}
		return t.Asked + " could not be opened — showing the closest folder above it that could.", true
	default:
		return "", false
	}
}

// folderName is what the folder is called at the top of its own listing. The
// top of a drive has no name of its own, so it keeps the whole path.
func folderName(dir string) string {
	if len(localpath.Crumbs(dir)) <= 2 {
		return dir
	}
	return filepath.Base(dir)
}

// pasteHint names the step people are missing when a pasted path does not
// work: in Explorer and Finder, copying a path is a modified Copy, not the
// plain one. Someone tried exactly this during a Windows demo — and had
// neither a way to get the path nor anywhere to paste it; the box above this
// hint is the other half of the fix.
func pasteHint() string {
	switch runtime.GOOS {
	case "windows":
		return `In Explorer, click the folder once and press Ctrl+Shift+C ("Copy as path"), then paste it here. The quotes it adds are fine.`
	case "darwin":
		return "In Finder, click the folder once and press Option-Command-C to copy its path, then paste it here."
	default:
		return "Paste the full path to a folder, or pick one below."
	}
}

func pathPlaceholder() string {
	switch runtime.GOOS {
	case "windows":
		return `D:\Work\Project`
	case "darwin":
		return "/Users/you/Documents/Project"
	default:
		return "/home/you/documents/project"
	}
}

func volumesHeading() string {
	if runtime.GOOS == "windows" {
		return "Drives"
	}
	return "Volumes"
}

// folderPaths is just the paths of the configured folders, which is all the
// picker and the breadth check need of them.
func folderPaths(folders []config.Folder) []string {
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		out = append(out, f.Path)
	}
	return out
}

// containsFolder reports whether path is already configured. It compares the
// way the platform does — D:\Work and d:\work are one folder on Windows, and
// adding both would sync it twice under two names.
func containsFolder(configured []string, path string) bool {
	for _, p := range configured {
		if localpath.SameFolder(p, path) {
			return true
		}
	}
	return false
}

// addFolder takes a folder from a Sync button or from the path box and
// configures it. A choice broad enough to be a mis-click — a whole drive, a
// home or system folder, or one overlapping a folder already synced — is
// shown as a question first (see confirmQuestion) and only added once the
// answer comes back with confirmed=1.
func (s *Server) addFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// The path arrives either from a Sync button, where it is already a
	// folder on this machine, or typed into the path box, where it is
	// whatever was pasted — quotes, file:// URL, %USERPROFILE% and all.
	path := filepath.Clean(localpath.Normalize(r.FormValue("path")))
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		s.renderError(w, http.StatusBadRequest, "That is not a folder",
			"There is no folder at "+path+" on this computer. Check the path and choose it again.")
		return
	}

	s.Mu.Lock()
	configured := folderPaths(s.Config.Folders)
	s.Mu.Unlock()

	if containsFolder(configured, path) {
		s.redirectHomeOrLink(w, r) // already set up; nothing to add
		return
	}
	if warn := localpath.Check(path, configured); warn.Broad() && r.FormValue("confirmed") != "1" {
		s.render(w, "confirm", s.confirmQuestion(path, warn))
		return
	}

	s.Mu.Lock()
	added := !containsFolder(folderPaths(s.Config.Folders), path)
	if added {
		s.Config.Folders = append(s.Config.Folders, config.Folder{ID: config.NewID(), Path: path})
		err = s.SaveConfig()
	}
	s.Mu.Unlock()

	if err != nil {
		s.Log.Error("save config after adding folder", "err", err)
	}
	if added && s.Wake != nil {
		s.Wake()
	}
	s.redirectHomeOrLink(w, r)
}

type confirmPage struct {
	Title, Message, Action string
	Path, Back             string
	HomeURL                string
}

// confirmQuestion is the question asked before a broad folder is synced. Each
// one names what would actually happen to the files, because "are you sure?"
// on its own is a question nobody can answer. None of them refuses: syncing a
// whole drive is a legitimate thing to want, just not by accident, and the way
// back is to the folder itself so the next click can be a folder inside it.
func (s *Server) confirmQuestion(path string, warn localpath.Warning) confirmPage {
	page := confirmPage{
		Path:    path,
		Action:  "Sync it anyway",
		Back:    "/browse?path=" + url.QueryEscape(path),
		HomeURL: s.cabinetURL(),
	}
	switch warn.Scope {
	case localpath.WholeVolume:
		page.Title = "Sync the whole of " + path + "?"
		page.Message = "Everything stored here would be uploaded to your vault — programs and system files included, not only your documents. Most people pick a folder inside it instead."
	case localpath.HomeFolder:
		page.Title = "Sync your whole home folder?"
		page.Message = "Alongside your documents this holds application data and settings, which are of no use on another computer and can be very large. A folder inside it is usually what you want."
	case localpath.SystemFolder:
		page.Title = "This folder belongs to the system"
		page.Message = "It holds installed programs rather than anything you wrote. Syncing it uploads a great many files that will not work on another computer."
	case localpath.Contains:
		page.Title = "A folder inside this one is already synced"
		page.Message = warn.Other + " is synced on its own. Syncing this one as well would upload those same files a second time, under a second name in your vault."
	case localpath.ContainedBy:
		page.Title = "This folder is inside one that is already synced"
		page.Message = "It sits in " + warn.Other + ", which is synced as a whole, so these files are already going up. Adding it again would upload them a second time, under a second name in your vault."
	case localpath.Ordinary:
		page.Title, page.Message = "Sync this folder?", path // never reached: Broad() is checked first
	}
	return page
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

// cabinetURL reads the cabinet address under the lock it shares with the
// sync loop.
func (s *Server) cabinetURL() string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.Config.EffectiveCabinetURL()
}

// renderError shows a failure as a regular page of the client instead of a
// bare plain-text body, with a way back to the home page.
func (s *Server) renderError(w http.ResponseWriter, status int, title, message string) {
	w.WriteHeader(status)
	s.render(w, "error", errorPage{Title: title, Message: message, HomeURL: s.cabinetURL()})
}

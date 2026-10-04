package localpath

import (
	"os"
	"runtime"
)

// Scope says why a chosen folder is a broad choice. The picker asks about
// these rather than refusing them: syncing a whole drive is unusual but
// legitimate, while doing it because "D:" happened to sit one click away
// from a Sync button is not what anyone meant.
type Scope int

const (
	// Ordinary: nothing to ask about.
	Ordinary Scope = iota
	// WholeVolume: the top of a drive — everything on it.
	WholeVolume
	// HomeFolder: everything belonging to this user, including the
	// application data under it.
	HomeFolder
	// SystemFolder: Windows, Program Files, /usr — installed programs rather
	// than anyone's documents.
	SystemFolder
	// Contains: a folder already being synced sits inside this one, so its
	// files would go up a second time under a second name.
	Contains
	// ContainedBy: this folder sits inside one already being synced, same
	// duplication the other way round.
	ContainedBy
)

// Warning is what Check found. Other names the already-synced folder a
// nesting warning is about, and is empty for the rest.
type Warning struct {
	Scope Scope
	Other string
}

// Broad reports whether there is anything to ask about at all.
func (w Warning) Broad() bool { return w.Scope != Ordinary }

// Check classifies a folder the person is about to sync against the folders
// already configured. It reports one reason, the broadest that applies: two
// warnings on one screen is a screen nobody reads.
func Check(dir string, configured []string) Warning {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return check(dir, configured, systemFolders(), home, runtime.GOOS)
}

func check(dir string, configured, system []string, home, goos string) Warning {
	for _, s := range system {
		if insideOn(dir, s, goos) {
			return Warning{Scope: SystemFolder}
		}
	}
	if len(components(dir)) == 1 {
		return Warning{Scope: WholeVolume}
	}
	if home != "" && sameFolderOn(dir, home, goos) {
		return Warning{Scope: HomeFolder}
	}
	for _, other := range configured {
		if sameFolderOn(dir, other, goos) {
			continue // already synced; not this screen's problem
		}
		if insideOn(dir, other, goos) {
			return Warning{Scope: ContainedBy, Other: other}
		}
		if insideOn(other, dir, goos) {
			return Warning{Scope: Contains, Other: other}
		}
	}
	return Warning{}
}

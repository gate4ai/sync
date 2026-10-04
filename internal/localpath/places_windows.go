package localpath

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// QuickAccess is the handful of folders a person is most likely to mean,
// asked of Windows itself rather than guessed from the home directory: once
// OneDrive is set up, Documents and Desktop live inside it, and a guessed
// %USERPROFILE%\Documents would be a stale folder nobody saves into.
func QuickAccess() []Place {
	var out []Place
	seen := map[string]bool{}
	add := func(name, path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		seen[path] = true
		out = append(out, Place{Name: name, Path: path})
	}
	known := func(name string, id *windows.KNOWNFOLDERID) {
		p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return
		}
		add(name, p)
	}
	known("Desktop", windows.FOLDERID_Desktop)
	known("Documents", windows.FOLDERID_Documents)
	known("Downloads", windows.FOLDERID_Downloads)
	// OneDrive is not a known folder: its root is published only as an
	// environment variable, one per account that is signed in.
	add("OneDrive", os.Getenv("OneDrive"))
	add("OneDrive for work", os.Getenv("OneDriveCommercial"))
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Base(home), home)
	}
	return out
}

// Volumes lists every drive letter Windows reports. A letter that cannot be
// read — an empty card reader, a network drive that is not connected — is
// still listed, with what is known about it, rather than hidden: a drive
// missing from this screen looks like the client cannot see it at all.
func Volumes() []Place {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []Place
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		kind := windows.GetDriveType(rootPtr)
		if kind == windows.DRIVE_NO_ROOT_DIR {
			continue
		}
		place := Place{Name: root[:2], Path: root, Label: driveKind(kind)}
		// A disconnected network drive makes both calls below block for
		// seconds, which would hold up the whole page; its letter and kind
		// are enough to click on.
		if kind != windows.DRIVE_REMOTE {
			if label := volumeLabel(rootPtr); label != "" {
				place.Label = label
			}
			var free, total uint64
			if err := windows.GetDiskFreeSpaceEx(rootPtr, &free, &total, nil); err == nil && total > 0 {
				place.Detail = humanBytes(free) + " free of " + humanBytes(total)
			}
		}
		out = append(out, place)
	}
	return out
}

func volumeLabel(rootPtr *uint16) string {
	var buf [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumeInformation(rootPtr, &buf[0], uint32(len(buf)), nil, nil, nil, nil, 0); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

// driveKind is the fallback name for a volume with no label of its own, in
// the words Explorer uses for the same thing.
func driveKind(kind uint32) string {
	switch kind {
	case windows.DRIVE_REMOVABLE:
		return "Removable drive"
	case windows.DRIVE_REMOTE:
		return "Network drive"
	case windows.DRIVE_CDROM:
		return "CD/DVD drive"
	case windows.DRIVE_RAMDISK:
		return "RAM disk"
	default:
		return "Local disk"
	}
}

// systemFolders are the folders holding Windows and the programs installed
// on it. Syncing one is almost always a mis-click, and the picker says so
// before it happens.
func systemFolders() []string {
	var out []string
	for _, env := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData", "LOCALAPPDATA", "APPDATA"} {
		if v := os.Getenv(env); v != "" {
			out = append(out, filepath.Clean(v))
		}
	}
	return out
}

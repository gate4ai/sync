//go:build linux || darwin

package localpath

import (
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// QuickAccess is the handful of folders a person is most likely to mean.
func QuickAccess() []Place {
	var out []Place
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	add := func(name, path string) {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		out = append(out, Place{Name: name, Path: path})
	}
	add(filepath.Base(home), home)
	for _, name := range []string{"Desktop", "Documents", "Downloads"} {
		add(name, filepath.Join(home, name))
	}
	return out
}

// Volumes lists the root plus whatever is mounted under the directories this
// platform mounts removable media and external disks in.
func Volumes() []Place {
	out := []Place{withSpace(Place{Name: "/", Label: "File system", Path: "/"})}
	seen := map[string]bool{"/": true}
	for _, dir := range mountDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, withSpace(Place{Name: e.Name(), Path: path}))
		}
	}
	return out
}

// mountDirs is where this platform puts mounted volumes: one place on macOS,
// and on Linux whichever of the usual three the distribution picked.
func mountDirs() []string {
	if runtime.GOOS == "darwin" {
		return []string{"/Volumes"}
	}
	dirs := []string{"/mnt", "/media"}
	if u := os.Getenv("USER"); u != "" {
		dirs = append(dirs, filepath.Join("/media", u), filepath.Join("/run/media", u))
	}
	return dirs
}

func withSpace(p Place) Place {
	var st unix.Statfs_t
	if err := unix.Statfs(p.Path, &st); err != nil {
		return p
	}
	block := uint64(st.Bsize)
	free, total := st.Bavail*block, st.Blocks*block
	if total == 0 {
		return p
	}
	p.Detail = humanBytes(free) + " free of " + humanBytes(total)
	return p
}

// systemFolders are the folders holding the operating system and the
// programs installed on it, rather than anybody's documents.
func systemFolders() []string {
	if runtime.GOOS == "darwin" {
		return []string{"/System", "/Library", "/Applications", "/usr", "/bin", "/sbin", "/private", "/Volumes/Preboot"}
	}
	// Not /run: /run/media is where most distributions mount removable
	// drives, and a USB stick is exactly the kind of folder someone means.
	return []string{"/usr", "/etc", "/var", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/proc", "/sys", "/dev", "/opt", "/snap"}
}

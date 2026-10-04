// Package localpath is the folder picker's knowledge of this machine: which
// drives and well-known folders it can start from, how to read a path a
// person pasted out of Explorer or Finder, and which choices are broad
// enough to ask about before syncing them.
//
// It lives apart from internal/webui because none of it is about HTTP, and
// because the Windows rules are the ones that matter most here while the
// tests that cover them run on Linux — every function that can be is split
// into a thin exported wrapper over a version taking the platform as an
// argument, so a Windows path can be checked from any machine.
package localpath

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Normalize turns what someone typed or pasted into a path this machine can
// open. Explorer's "Copy as path" (Ctrl+Shift+C) wraps the path in quotes, a
// path dragged out of a browser arrives as a percent-encoded file:// URL,
// and people reasonably type %USERPROFILE%, $HOME or ~ — os.Stat accepts
// none of those verbatim, so without this step the one shortcut Explorer
// offers for "tell something else where this folder is" does not work.
//
// It is text work only: the caller still passes the result through
// filepath.Clean and os.Stat (see Resolve).
func Normalize(raw string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return normalizeOn(raw, runtime.GOOS, home)
}

func normalizeOn(raw, goos, home string) string {
	windows := goos == "windows"
	s := strings.TrimSpace(raw)
	s = unquote(s)
	s = unfileURL(s, windows)
	s = expandVars(s, windows)
	s = expandHome(s, home, windows)
	if windows {
		s = windowsShape(s)
	}
	return s
}

// unquote drops one balanced pair of quotes. "Copy as path" always produces
// them, and a path copied out of a terminal often has them too. Only a
// balanced pair goes: a lone quote is more likely part of a folder's name
// than a stray delimiter.
func unquote(s string) string {
	for _, q := range []string{`"`, `'`} {
		for len(s) >= 2 && strings.HasPrefix(s, q) && strings.HasSuffix(s, q) {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

// unfileURL unwraps the file:// URL a path becomes when it travels through a
// browser — "file:///D:/%D0%9E%D1%82%D1%87%D1%91%D1%82%D1%8B" for a folder
// with a non-Latin name.
func unfileURL(s string, windows bool) string {
	if len(s) < 7 || !strings.EqualFold(s[:7], "file://") {
		return s
	}
	rest := percentDecode(strings.TrimPrefix(s[7:], "localhost"))
	// A Windows file URL keeps the drive behind the authority's slash:
	// file:///D:/x unwraps to /D:/x, which is not a path on any drive.
	if windows && len(rest) >= 3 && rest[0] == '/' && rest[2] == ':' {
		rest = rest[1:]
	}
	return rest
}

// percentDecode is url.PathUnescape's job, done here so that a stray "%" in
// a folder's name — legal on every platform the client runs on — leaves the
// rest of the path alone instead of failing the whole paste.
func percentDecode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+2 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		hi, lo := unhex(s[i+1]), unhex(s[i+2])
		if hi < 0 || lo < 0 {
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(byte(hi<<4 | lo))
		i += 2
	}
	return b.String()
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

var (
	winVarRE  = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_()]*)%`)
	unixVarRE = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)
)

// expandVars substitutes environment variables, but only the ones that are
// actually set: an unset %USERPROFLE% stays in the text so the "no such
// folder" message shows the typo back, rather than silently collapsing to a
// shorter path that happens to exist.
func expandVars(s string, windows bool) string {
	re := unixVarRE
	if windows {
		re = winVarRE
	}
	return re.ReplaceAllStringFunc(s, func(m string) string {
		name := re.FindStringSubmatch(m)[1]
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v
		}
		return m
	})
}

// expandHome replaces a leading ~ with the home directory. Only a leading
// one, and only when it stands alone or starts a path: "~" names a folder
// just fine in the middle of one.
func expandHome(s, home string, windows bool) string {
	if home == "" || !strings.HasPrefix(s, "~") {
		return s
	}
	rest := s[1:]
	if rest == "" {
		return home
	}
	if rest[0] == '/' || (windows && rest[0] == '\\') {
		return home + rest
	}
	return s
}

// windowsShape puts a Windows path into the form os.Stat wants: back
// slashes, one between parts — except the pair that opens a UNC share — an
// upper-case drive letter, and a root that keeps its slash, because "D:"
// alone names the current directory on D: rather than the top of it.
func windowsShape(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "/", `\`)
	unc := strings.HasPrefix(s, `\\`)
	for strings.Contains(s, `\\`) {
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	if unc {
		s = `\` + s
	}
	if len(s) >= 2 && s[1] == ':' {
		s = strings.ToUpper(s[:1]) + s[1:]
		if len(s) == 2 {
			return s + `\`
		}
	}
	if len(s) > 3 && strings.HasSuffix(s, `\`) {
		s = strings.TrimRight(s, `\`)
	}
	return s
}

// Outcome says how close Resolve got to the path it was handed.
type Outcome int

const (
	// Found: Dir is the folder that was asked for.
	Found Outcome = iota
	// WasFile: the path named a file, and Dir is the folder holding it.
	// Pasting the path of a document is a normal way to mean "that folder",
	// and Explorer's own "Copy as path" on a selected file produces exactly
	// this.
	WasFile
	// Missing: there is nothing at that path. Dir is the nearest folder
	// above it that does exist.
	Missing
	// Denied: the path, or everything between it and Dir, cannot be read.
	Denied
)

// Target is where the picker should look after reading what it was given.
// Dir is always somewhere that can be listed, or empty if even the roots
// cannot be — a typo never leaves the picker with nothing to show.
type Target struct {
	Dir     string
	Asked   string
	Outcome Outcome
}

// Resolve reads a pasted, typed or clicked path and decides which folder the
// picker shows for it.
func Resolve(raw string) Target {
	asked := Normalize(raw)
	if asked == "" {
		return Target{}
	}
	asked = filepath.Clean(asked)
	t := Target{Asked: asked}

	info, err := os.Stat(asked)
	switch {
	case err == nil && info.IsDir():
		if readable(asked) {
			t.Dir, t.Outcome = asked, Found
			return t
		}
		t.Dir, t.Outcome = nearestReadable(parent(asked)), Denied
		return t
	case err == nil:
		holder := filepath.Dir(asked)
		if readable(holder) {
			t.Dir, t.Outcome = holder, WasFile
			return t
		}
		t.Dir, t.Outcome = nearestReadable(parent(holder)), Denied
		return t
	case errors.Is(err, fs.ErrPermission):
		t.Dir, t.Outcome = nearestReadable(parent(asked)), Denied
		return t
	default:
		t.Dir, t.Outcome = nearestReadable(parent(asked)), Missing
		return t
	}
}

// readable reports whether this folder can be listed. Existing is not
// enough: on Windows a drive letter is there for a card reader with no card
// in it, and plenty of folders under C:\ refuse to be opened.
func readable(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if _, err := f.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return true
}

// nearestReadable walks up until a folder can actually be listed, so a typo
// deep inside a pasted path lands on the closest real folder above it.
func nearestReadable(dir string) string {
	for dir != "" {
		if readable(dir) {
			return dir
		}
		dir = parent(dir)
	}
	return ""
}

// parent is filepath.Dir with the top of a volume reported as "" rather than
// itself — that is how the picker knows to offer the roots screen instead of
// an "up" link that goes nowhere, which is what used to make a folder on D:
// unreachable from C:.
func parent(dir string) string {
	p := filepath.Dir(dir)
	if p == dir {
		return ""
	}
	return p
}

// Parent is parent, exported for the picker's "up" link.
func Parent(dir string) string { return parent(dir) }

// Crumb is one step of the trail above the listing. Path is empty for the
// first step, the roots screen, which has no path of its own.
type Crumb struct {
	Name string
	Path string
}

// Crumbs breaks dir into that trail. Every trail starts at the roots screen,
// so there is always one click back out of a drive, and every step in
// between is clickable — walking up five levels used to be five presses of
// "..".
func Crumbs(dir string) []Crumb { return crumbsOn(dir, runtime.GOOS) }

func crumbsOn(dir, goos string) []Crumb {
	out := []Crumb{{Name: rootsNameOn(goos)}}
	if dir == "" {
		return out
	}
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	base, rest := volumeCrumb(dir, goos, sep)
	if base == "" {
		return out
	}
	name := base
	if name != sep {
		name = strings.TrimSuffix(name, sep)
	}
	out = append(out, Crumb{Name: name, Path: base})
	for _, part := range strings.Split(rest, sep) {
		if part == "" {
			continue
		}
		if !strings.HasSuffix(base, sep) {
			base += sep
		}
		base += part
		out = append(out, Crumb{Name: part, Path: base})
	}
	return out
}

// volumeCrumb splits off the part of dir that cannot be walked into one
// component at a time — a drive letter, a UNC share, or the unix root — and
// returns it with whatever is left below it.
func volumeCrumb(dir, goos, sep string) (base, rest string) {
	if goos != "windows" {
		if !strings.HasPrefix(dir, "/") {
			return "", ""
		}
		return "/", strings.TrimPrefix(dir, "/")
	}
	if len(dir) >= 2 && dir[1] == ':' {
		return dir[:2] + sep, strings.TrimPrefix(dir[2:], sep)
	}
	if strings.HasPrefix(dir, `\\`) {
		parts := strings.SplitN(strings.TrimPrefix(dir, `\\`), sep, 3)
		if len(parts) < 2 {
			return dir, ""
		}
		base = `\\` + parts[0] + sep + parts[1]
		if len(parts) == 3 {
			rest = parts[2]
		}
		return base, rest
	}
	return "", ""
}

// RootsName is what the screen listing drives and well-known folders is
// called — the name the platform's own file manager gives it, so it is the
// thing people are already looking for.
func RootsName() string { return rootsNameOn(runtime.GOOS) }

func rootsNameOn(goos string) string {
	switch goos {
	case "windows":
		return "This PC"
	case "darwin":
		return "Locations"
	default:
		return "Computer"
	}
}

// caseInsensitive reports whether this platform hands back the same folder
// for two paths differing only in case. Windows always does; macOS does on a
// default-formatted volume. Treating macOS as insensitive can in principle
// refuse a second, genuinely distinct folder on a case-sensitive volume,
// which is rare — syncing one folder twice under two names, the other
// mistake, is both likelier and worse.
func caseInsensitive(goos string) bool { return goos == "windows" || goos == "darwin" }

// SameFolder reports whether two paths name one folder.
func SameFolder(a, b string) bool { return sameFolderOn(a, b, runtime.GOOS) }

func sameFolderOn(a, b, goos string) bool {
	ca, cb := components(a), components(b)
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if !sameName(ca[i], cb[i], goos) {
			return false
		}
	}
	return true
}

// Inside reports whether child is dir itself or sits somewhere below it. It
// compares whole components, so /home/alexa is not inside /home/alex.
func Inside(child, dir string) bool { return insideOn(child, dir, runtime.GOOS) }

func insideOn(child, dir, goos string) bool {
	c, d := components(child), components(dir)
	if len(c) < len(d) {
		return false
	}
	for i := range d {
		if !sameName(c[i], d[i], goos) {
			return false
		}
	}
	return true
}

func sameName(a, b, goos string) bool {
	if caseInsensitive(goos) {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// components splits a path into its volume and the names below it. The
// volume is always the first element — empty on unix, where it is the shared
// root that makes every absolute path comparable.
func components(p string) []string {
	p = filepath.Clean(p)
	vol := volumeName(p)
	rest := strings.Trim(p[len(vol):], `/\`)
	out := []string{vol}
	if rest == "" {
		return out
	}
	return append(out, strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })...)
}

// volumeName is filepath.VolumeName's job, done here because the picker
// compares Windows paths on whichever machine the tests run on, and
// filepath only knows about drive letters when it was built for Windows.
func volumeName(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		return p[:2]
	}
	if strings.HasPrefix(p, `\\`) {
		parts := strings.SplitN(strings.TrimPrefix(p, `\\`), `\`, 3)
		if len(parts) >= 2 {
			return `\\` + parts[0] + `\` + parts[1]
		}
	}
	return ""
}

// humanBytes is the free/total size shown next to a drive, in the units a
// drive is sold in (powers of 1000, like Explorer's own "free of").
func humanBytes(n uint64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	var suffix string
	for _, u := range units {
		v /= unit
		suffix = u
		if v < unit {
			break
		}
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, suffix)
	}
	return fmt.Sprintf("%.0f %s", v, suffix)
}

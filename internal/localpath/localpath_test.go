package localpath

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The Windows cases are the ones this package exists for, and they run on
// Linux like everything else in CI — hence normalizeOn and friends taking the
// platform as an argument.
func TestNormalizeWindows(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\Sergey`)
	home := `C:\Users\Sergey`

	tests := []struct {
		name, in, want string
	}{
		{"copy as path wraps it in quotes", `"D:\Work\Project 2026"`, `D:\Work\Project 2026`},
		{"single quotes from a shell", `'D:\Work'`, `D:\Work`},
		{"surrounding whitespace", "  D:\\Work\t\n", `D:\Work`},
		{"quotes with space inside", `" D:\Work "`, `D:\Work`},
		{"forward slashes", `D:/Work/Project`, `D:\Work\Project`},
		{"repeated separators", `D:\\Work\\\Project`, `D:\Work\Project`},
		{"lower-case drive letter", `d:\work`, `D:\work`},
		{"bare drive letter means its root", `D:`, `D:\`},
		{"trailing separator", `D:\Work\`, `D:\Work`},
		{"drive root keeps its separator", `D:\`, `D:\`},
		{"UNC share keeps its leading pair", `\\nas\share\Work`, `\\nas\share\Work`},
		{"file URL", `file:///D:/Work`, `D:\Work`},
		{"file URL percent-encoded", `file:///D:/%D0%9E%D1%82%D1%87%D1%91%D1%82%D1%8B`, `D:\Отчёты`},
		{"file URL with localhost", `file://localhost/D:/Work`, `D:\Work`},
		{"environment variable", `%USERPROFILE%\Documents`, `C:\Users\Sergey\Documents`},
		{"tilde", `~\Documents`, `C:\Users\Sergey\Documents`},
		{"tilde alone", `~`, `C:\Users\Sergey`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeOn(tt.in, "windows", home); got != tt.want {
				t.Errorf("normalizeOn(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// An unset variable stays in the text: collapsing %USERPROFLE% to nothing
// would turn a typo into \Documents, a path that may well exist and is not
// what was asked for, and the "no such folder" message could no longer show
// the person what they actually typed.
func TestNormalizeKeepsAnUnsetVariable(t *testing.T) {
	in := `%USERPROFLE%\Documents`
	if got := normalizeOn(in, "windows", `C:\Users\S`); got != in {
		t.Errorf("normalizeOn(%q) = %q, want it unchanged", in, got)
	}
}

func TestNormalizeUnix(t *testing.T) {
	t.Setenv("MYDOCS", "/srv/docs")
	tests := []struct {
		name, in, want string
	}{
		{"quotes", `"/home/alex/notes"`, "/home/alex/notes"},
		{"tilde", "~/notes", "/home/alex/notes"},
		{"variable", "$MYDOCS/2026", "/srv/docs/2026"},
		{"braced variable", "${MYDOCS}/2026", "/srv/docs/2026"},
		{"file URL", "file:///home/alex/notes", "/home/alex/notes"},
		{"backslashes are left alone", `/home/alex/a\b`, `/home/alex/a\b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeOn(tt.in, "linux", "/home/alex"); got != tt.want {
				t.Errorf("normalizeOn(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveFindsAFolder(t *testing.T) {
	dir := t.TempDir()
	got := Resolve(dir)
	if got.Dir != dir || got.Outcome != Found {
		t.Errorf("Resolve(%q) = %+v, want the folder itself and Found", dir, got)
	}
}

// Explorer's "Copy as path" on a selected file is how a lot of people will
// answer "where is the folder?", so a file's path means the folder holding it.
func TestResolveTakesTheFolderHoldingAFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "estimate.xlsx")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Resolve(file)
	if got.Dir != dir || got.Outcome != WasFile {
		t.Errorf("Resolve(%q) = %+v, want %q and WasFile", file, got, dir)
	}
}

func TestResolveFallsBackToTheNearestExistingFolder(t *testing.T) {
	dir := t.TempDir()
	got := Resolve(filepath.Join(dir, "Rabta", "Proekt"))
	if got.Dir != dir || got.Outcome != Missing {
		t.Errorf("Resolve of a typo = %+v, want %q and Missing", got, dir)
	}
	if !strings.HasSuffix(got.Asked, filepath.Join("Rabta", "Proekt")) {
		t.Errorf("Asked = %q, want the path that was typed so it can be shown back", got.Asked)
	}
}

func TestResolveOfNothingIsEmpty(t *testing.T) {
	if got := Resolve("   "); got.Dir != "" || got.Outcome != Found {
		t.Errorf("Resolve(blank) = %+v, want the zero value", got)
	}
}

func TestResolveOfAnUnreadableFolderStepsUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions are ACLs here, which a mode of 0 does not touch")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder, so there is nothing to refuse")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	got := Resolve(filepath.Join(locked, "inside"))
	if got.Dir != dir || got.Outcome != Denied {
		t.Errorf("Resolve inside an unreadable folder = %+v, want %q and Denied", got, dir)
	}
}

func TestCrumbs(t *testing.T) {
	tests := []struct {
		name, dir, goos string
		want            []Crumb
	}{
		{
			name: "windows path", dir: `D:\Work\Clients\Project`, goos: "windows",
			want: []Crumb{
				{Name: "This PC"},
				{Name: "D:", Path: `D:\`},
				{Name: "Work", Path: `D:\Work`},
				{Name: "Clients", Path: `D:\Work\Clients`},
				{Name: "Project", Path: `D:\Work\Clients\Project`},
			},
		},
		{
			name: "drive root", dir: `D:\`, goos: "windows",
			want: []Crumb{{Name: "This PC"}, {Name: "D:", Path: `D:\`}},
		},
		{
			name: "UNC share", dir: `\\nas\share\Work`, goos: "windows",
			want: []Crumb{
				{Name: "This PC"},
				{Name: `\\nas\share`, Path: `\\nas\share`},
				{Name: "Work", Path: `\\nas\share\Work`},
			},
		},
		{
			name: "unix path", dir: "/home/alex/notes", goos: "linux",
			want: []Crumb{
				{Name: "Computer"},
				{Name: "/", Path: "/"},
				{Name: "home", Path: "/home"},
				{Name: "alex", Path: "/home/alex"},
				{Name: "notes", Path: "/home/alex/notes"},
			},
		},
		{
			name: "roots screen itself", dir: "", goos: "windows",
			want: []Crumb{{Name: "This PC"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := crumbsOn(tt.dir, tt.goos); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("crumbsOn(%q, %q) =\n%+v\nwant\n%+v", tt.dir, tt.goos, got, tt.want)
			}
		})
	}
}

// Two mounts for one folder is the bug this prevents: on Windows D:\Work and
// d:\work are the same folder, and adding both would upload it twice.
func TestSameFolderIgnoresCaseOnWindowsOnly(t *testing.T) {
	if !sameFolderOn(`D:\Work`, `d:\work`, "windows") {
		t.Error("windows: D:\\Work and d:\\work are one folder")
	}
	if sameFolderOn("/home/a/Work", "/home/a/work", "linux") {
		t.Error("linux: Work and work are two folders")
	}
	if sameFolderOn(`D:\Work`, `D:\Work\Sub`, "windows") {
		t.Error("a folder is not the same as its subfolder")
	}
}

func TestInsideComparesWholeNames(t *testing.T) {
	if !insideOn("/home/alex/notes", "/home/alex", "linux") {
		t.Error("notes is inside /home/alex")
	}
	if insideOn("/home/alexa", "/home/alex", "linux") {
		t.Error("/home/alexa is a different folder, not one inside /home/alex")
	}
	if !insideOn("/home/alex", "/home/alex", "linux") {
		t.Error("a folder counts as inside itself")
	}
	if !insideOn("/home/alex", "/", "linux") {
		t.Error("everything is inside the root")
	}
	if insideOn(`C:\Work`, `D:\`, "windows") {
		t.Error("a folder on C: is not inside D:")
	}
}

func TestCheckWarnsAboutBroadChoices(t *testing.T) {
	system := []string{`C:\Windows`, `C:\Program Files`}
	home := `C:\Users\Sergey`
	configured := []string{`D:\Work\Clients`}

	tests := []struct {
		name, dir string
		want      Scope
		other     string
	}{
		{"whole drive", `D:\`, WholeVolume, ""},
		{"system folder", `C:\Windows\System32`, SystemFolder, ""},
		{"program files itself", `C:\Program Files`, SystemFolder, ""},
		{"home folder", `C:\Users\Sergey`, HomeFolder, ""},
		{"holds a synced folder", `D:\Work`, Contains, `D:\Work\Clients`},
		{"sits in a synced folder", `D:\Work\Clients\Project`, ContainedBy, `D:\Work\Clients`},
		{"already synced, nothing to warn about", `D:\Work\Clients`, Ordinary, ""},
		{"ordinary folder", `D:\Photos`, Ordinary, ""},
		{"ordinary folder under the home one", `C:\Users\Sergey\Notes`, Ordinary, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := check(tt.dir, configured, system, home, "windows")
			if got.Scope != tt.want || got.Other != tt.other {
				t.Errorf("check(%q) = %+v, want scope %v other %q", tt.dir, got, tt.want, tt.other)
			}
			if got.Broad() != (tt.want != Ordinary) {
				t.Errorf("check(%q).Broad() = %v", tt.dir, got.Broad())
			}
		})
	}
}

// The unix root is a volume too: "/" is every file on the machine.
func TestCheckTreatsTheUnixRootAsAWholeVolume(t *testing.T) {
	if got := check("/", nil, nil, "/home/alex", "linux"); got.Scope != WholeVolume {
		t.Errorf("check(\"/\") = %+v, want WholeVolume", got)
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   uint64
		want string
	}{
		{512, "512 B"},
		{41 * 1000 * 1000 * 1000, "41 GB"},
		{1500 * 1000 * 1000 * 1000, "1.5 TB"},
		{1000 * 1000 * 1000 * 1000, "1.0 TB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

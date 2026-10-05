package localpath

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// The drive list is the part of this package that cannot be checked by
// reasoning about strings: it is whatever the operating system reports. These
// two tests run on every platform CI covers, which is the point — until the
// test job grew a Windows and a macOS runner, the Windows half of this package
// was compiled and never executed.

func TestVolumesAndQuickAccessAreUsable(t *testing.T) {
	volumes := Volumes()
	if len(volumes) == 0 {
		t.Fatal("no volumes at all: the roots screen would have nothing to click")
	}
	// Not every volume has to be readable — a card reader with no card in it
	// is listed on purpose, so its letter is still somewhere to click — but
	// the one this machine is running from certainly is.
	readableFound := false
	for _, v := range volumes {
		if v.Name == "" || v.Path == "" {
			t.Errorf("volume with nothing to show: %+v", v)
		}
		if readable(v.Path) {
			readableFound = true
		}
	}
	if !readableFound {
		t.Errorf("not one of %d volumes could be listed", len(volumes))
	}

	for _, p := range QuickAccess() {
		info, err := os.Stat(p.Path)
		if err != nil || !info.IsDir() {
			t.Errorf("quick access offers %q, which is not a folder: %v", p.Path, err)
		}
		if p.Name == "" {
			t.Errorf("quick access entry with no name: %+v", p)
		}
	}
}

// TestVolumesFindsTheOneMountedForThisRun checks the list against a volume
// created for this run with a name and a label chosen in advance — the only
// way to tell a correct GetVolumeInformation call from one that merely does
// not crash. CI mounts it (see .github/workflows/ci.yml); with no such volume
// there is nothing to compare against and the test skips.
func TestVolumesFindsTheOneMountedForThisRun(t *testing.T) {
	want := os.Getenv("GATE4AI_TEST_VOLUME")
	if want == "" {
		// A test that skips itself looks exactly like one that passed, and
		// `go test` without -v prints neither. CI mounts a volume on the two
		// platforms that can, so a missing one there means the step that
		// mounts it broke — which must be a failure, not a quiet skip.
		if os.Getenv("CI") != "" && (runtime.GOOS == "windows" || runtime.GOOS == "darwin") {
			t.Fatal("no volume was mounted for this run: see the workflow step that mounts one")
		}
		t.Skip("no test volume mounted for this run")
	}
	wantLabel := os.Getenv("GATE4AI_TEST_VOLUME_LABEL")

	volumes := Volumes()
	for _, v := range volumes {
		if !strings.EqualFold(v.Name, want) {
			continue
		}
		if wantLabel != "" && !strings.EqualFold(v.Label, wantLabel) {
			t.Errorf("Label = %q, want %q — the volume's own name was not read", v.Label, wantLabel)
		}
		if v.Detail == "" {
			t.Error("Detail is empty: the free space on the volume was not measured")
		}
		if !readable(v.Path) {
			t.Errorf("volume %q cannot be listed at %q", v.Name, v.Path)
		}
		return
	}
	t.Errorf("volume %q is missing from %+v", want, volumes)
}

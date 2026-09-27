package loop

import (
	"context"
	"slices"
	"testing"

	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/syncengine"
)

// fakePlanner accepts every path except those in refused, answering under
// version, and records what it was asked.
type fakePlanner struct {
	version string
	refused map[string]bool
	asked   [][]string
}

func (p *fakePlanner) Plan(_ context.Context, _ string, files []controlclient.PlanFile) (controlclient.PlanResult, error) {
	var paths []string
	res := controlclient.PlanResult{PolicyVersion: p.version, Accept: make([]bool, len(files))}
	for i, f := range files {
		paths = append(paths, f.Path)
		res.Accept[i] = !p.refused[f.Path]
	}
	p.asked = append(p.asked, paths)
	return res, nil
}

func TestServerFilterAsksOnlyAboutWhatItHasNotSeenUnderThisVersion(t *testing.T) {
	planner := &fakePlanner{version: "v1", refused: map[string]bool{"clip.mp4": true}}
	cache := &planCache{}
	filter := func(version string) *serverFilter {
		return &serverFilter{control: planner, folderID: "f", version: version, cache: cache}
	}
	files := []syncengine.File{{Path: "a.md", Size: 1}, {Path: "clip.mp4", Size: 9}}

	got, err := filter("v1").Accept(t.Context(), files)
	if err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("first Accept = %v, want [true false]", got)
	}

	// Same files, same version: answered from the cache.
	if _, err := filter("v1").Accept(t.Context(), files); err != nil {
		t.Fatalf("second Accept: %v", err)
	}
	if len(planner.asked) != 1 {
		t.Fatalf("asked %d times, want 1 (second call served from cache)", len(planner.asked))
	}

	// A resized file is a new question; the unchanged one is not.
	files[0].Size = 2
	if _, err := filter("v1").Accept(t.Context(), files); err != nil {
		t.Fatalf("third Accept: %v", err)
	}
	if !slices.Equal(planner.asked[1], []string{"a.md"}) {
		t.Errorf("third call asked about %v, want only the resized a.md", planner.asked[1])
	}

	// New settings version: everything is asked again.
	planner.version = "v2"
	planner.refused = map[string]bool{}
	got, err = filter("v2").Accept(t.Context(), files)
	if err != nil {
		t.Fatalf("fourth Accept: %v", err)
	}
	if !slices.Equal(got, []bool{true, true}) || len(planner.asked[2]) != 2 {
		t.Errorf("after the version changed: answers %v, asked %v; want both re-asked and accepted", got, planner.asked[2])
	}
}

// The rules can change between reading settings and asking; answers under a
// version the cache does not hold must not be mixed with its old ones.
func TestServerFilterStartsOverWhenTheServerAnswersUnderANewerVersion(t *testing.T) {
	planner := &fakePlanner{version: "v1"}
	cache := &planCache{}
	files := []syncengine.File{{Path: "a.md", Size: 1}, {Path: "b.md", Size: 1}}
	if _, err := (&serverFilter{control: planner, version: "v1", cache: cache}).Accept(t.Context(), files[:1]); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	planner.version = "v2"
	planner.refused = map[string]bool{"a.md": true}
	got, err := (&serverFilter{control: planner, version: "v1", cache: cache}).Accept(t.Context(), files)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !slices.Equal(got, []bool{false, true}) {
		t.Errorf("Accept = %v, want [false true] — a.md's stale v1 answer must not survive", got)
	}
	if cache.version != "v2" {
		t.Errorf("cache version = %q, want v2", cache.version)
	}
}

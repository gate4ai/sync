package loop

import (
	"context"

	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/syncengine"
)

// planChunk bounds one POST /api/sync/v1/plan; the server takes up to
// 50 000 files a request, so a first sync of a large folder is still a
// handful of round trips.
const planChunk = 5000

// planner is the one controlclient call serverFilter needs.
type planner interface {
	Plan(ctx context.Context, folderID string, files []controlclient.PlanFile) (controlclient.PlanResult, error)
}

// planCache is the server's answers for one folder, valid while the
// server's policy_version stays what it was when they were given.
type planCache struct {
	version string
	answers map[syncengine.File]bool
}

// planCaches outlives a single runOnce, keyed by folder ID, so a steady
// folder costs a plan request only for files that are new or changed size.
type planCaches map[string]*planCache

func (c planCaches) folder(folderID string) *planCache {
	pc, ok := c[folderID]
	if !ok {
		pc = &planCache{}
		c[folderID] = pc
	}
	return pc
}

// serverFilter is syncengine.Filter backed by POST /api/sync/v1/plan: the
// server decides which files take part; this only remembers its answers.
type serverFilter struct {
	control  planner
	folderID string
	// version is policy_version from this cycle's settings.
	version string
	cache   *planCache
}

func (f *serverFilter) Accept(ctx context.Context, files []syncengine.File) ([]bool, error) {
	// A second attempt covers the rules changing between reading settings
	// and asking: the answers then carry a newer version, and everything
	// cached under the old one has to be asked again.
	for attempt := 0; attempt < 2; attempt++ {
		if f.cache.version != f.version || f.cache.answers == nil {
			f.cache.version = f.version
			f.cache.answers = map[syncengine.File]bool{}
		}
		changed, err := f.askMissing(ctx, files)
		if err != nil {
			return nil, err
		}
		if !changed {
			break
		}
	}

	out := make([]bool, len(files))
	current := make(map[syncengine.File]bool, len(files))
	for i, file := range files {
		out[i] = f.cache.answers[file]
		if answer, ok := f.cache.answers[file]; ok {
			current[file] = answer
		}
	}
	// Only what the folder holds now stays cached, so renames and edits do
	// not grow the cache without bound.
	f.cache.answers = current
	return out, nil
}

// askMissing asks about every file the cache has no answer for. It reports
// changed when the server answered under a policy version other than the one
// the cache holds, having adopted that version without storing the answer.
func (f *serverFilter) askMissing(ctx context.Context, files []syncengine.File) (changed bool, err error) {
	var missing []syncengine.File
	for _, file := range files {
		if _, ok := f.cache.answers[file]; !ok {
			missing = append(missing, file)
		}
	}
	for start := 0; start < len(missing); start += planChunk {
		chunk := missing[start:min(start+planChunk, len(missing))]
		req := make([]controlclient.PlanFile, len(chunk))
		for i, file := range chunk {
			req[i] = controlclient.PlanFile{Path: file.Path, Size: file.Size}
		}
		res, err := f.control.Plan(ctx, f.folderID, req)
		if err != nil {
			return false, err
		}
		if res.PolicyVersion != f.cache.version {
			f.version = res.PolicyVersion
			return true, nil
		}
		for i, file := range chunk {
			f.cache.answers[file] = res.Accept[i]
		}
	}
	return false, nil
}

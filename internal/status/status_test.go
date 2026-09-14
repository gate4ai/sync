package status_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gate4ai/sync/internal/status"
)

func TestFreshStatusHasNoSyncAndNoError(t *testing.T) {
	var st status.Status
	snap := st.Snapshot()
	if !snap.LastSync.IsZero() {
		t.Errorf("LastSync = %v, want zero", snap.LastSync)
	}
	if snap.Error != "" {
		t.Errorf("Error = %q, want empty", snap.Error)
	}
}

func TestRecordSuccessClearsAnEarlierError(t *testing.T) {
	var st status.Status
	st.RecordError(errors.New("boom"), time.Now())
	st.RecordSuccess(time.Now())

	snap := st.Snapshot()
	if snap.Error != "" {
		t.Errorf("Error = %q, want cleared after a success", snap.Error)
	}
	if snap.LastSync.IsZero() {
		t.Error("LastSync is zero after RecordSuccess")
	}
}

func TestRecordErrorKeepsTheEarlierLastSync(t *testing.T) {
	var st status.Status
	firstSync := time.Now().Add(-time.Hour)
	st.RecordSuccess(firstSync)
	st.RecordError(errors.New("timeout"), time.Now())

	snap := st.Snapshot()
	if snap.Error != "timeout" {
		t.Errorf("Error = %q, want %q", snap.Error, "timeout")
	}
	if !snap.LastSync.Equal(firstSync) {
		t.Errorf("LastSync = %v, want the earlier success time %v preserved", snap.LastSync, firstSync)
	}
}

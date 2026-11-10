package v1alpha1

import "testing"

func TestReleaseStateEnded(t *testing.T) {
	for s, want := range map[ReleaseState]bool{
		ReleasePlanned: false, ReleaseWaiting: false, ReleaseRunning: false,
		ReleaseReleased: true, ReleaseAlreadyReleased: true, ReleaseNotNeeded: true,
		ReleaseNotApplicable: true, ReleaseDropped: true, ReleaseFailed: true,
	} {
		if s.Ended() != want {
			t.Errorf("%s.Ended() = %v", s, !want)
		}
	}
}

func TestTrimReleases(t *testing.T) {
	recs := []ReleaseRecord{
		{ID: 1, State: ReleaseReleased}, {ID: 2, State: ReleasePlanned},
		{ID: 3, State: ReleaseDropped}, {ID: 4, State: ReleaseRunning}, {ID: 5, State: ReleaseReleased},
	}
	got := TrimReleases(recs, 3)
	var ids []int64
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if len(ids) != 3 || ids[0] != 2 || ids[1] != 4 || ids[2] != 5 {
		t.Errorf("ids = %v, want [2 4 5]", ids)
	}
	if got := TrimReleases(recs, 10); len(got) != 5 {
		t.Errorf("under the limit trimmed: %d", len(got))
	}
	open := []ReleaseRecord{{ID: 1, State: ReleasePlanned}, {ID: 2, State: ReleaseWaiting}}
	if got := TrimReleases(open, 1); len(got) != 2 {
		t.Errorf("unended records dropped: %d", len(got))
	}
}

package v1alpha1

import "testing"

func TestCheckpointHead(t *testing.T) {
	cp := RunStatus{Checkpoint: &CheckpointStatus{SHA: "c1"}}
	cases := []struct {
		name string
		run  RunStatus
		sha  string
		want bool
	}{
		{"head is the checkpoint", cp, "c1", true},
		{"head moved past it", cp, "c2", false},
		{"no head", cp, "", false},
		{"no checkpoint", RunStatus{}, "c1", false},
		{"checkpoint without a SHA", RunStatus{Checkpoint: &CheckpointStatus{}}, "", false},
	}
	for _, c := range cases {
		if got := c.run.CheckpointHead(c.sha); got != c.want {
			t.Errorf("%s: CheckpointHead(%q) = %v, want %v", c.name, c.sha, got, c.want)
		}
	}
}

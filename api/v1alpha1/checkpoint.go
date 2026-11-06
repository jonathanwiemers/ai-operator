package v1alpha1

// CheckpointHead reports whether sha, a branch or merge request head, is the
// run's last checkpoint: work in progress after a cut-off, never ready,
// reviewed or merged. The recorded checkpoint may be stale, so it only
// counts while it is still the head.
func (s RunStatus) CheckpointHead(sha string) bool {
	return s.Checkpoint != nil && sha != "" && s.Checkpoint.SHA == sha
}

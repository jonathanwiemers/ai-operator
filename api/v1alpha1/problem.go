package v1alpha1

// ProblemIssue is the issue a pause of ap belongs to: the rule the Desk and
// the pilot use to group a pause into an issue's problem. It is PausedFor
// when set, else the single Active item with a hold. It is 0 while ap is not
// paused, and for a pause over the whole milestone (a budget, a red default
// branch, or more than one held issue). The pause's text is not read: it may
// name an issue that waits on the held one.
func ProblemIssue(ap *AutopilotStatus) int64 {
	if ap == nil || ap.Phase != AutopilotPaused {
		return 0
	}
	if ap.PausedFor != 0 {
		return ap.PausedFor
	}
	var held int64
	for _, it := range ap.Items {
		if it.State == AutopilotItemActive && it.Hold != nil {
			if held != 0 {
				return 0
			}
			held = it.IID
		}
	}
	return held
}

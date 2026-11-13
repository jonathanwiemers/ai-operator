package v1alpha1

import (
	"strconv"
	"time"
)

// FollowUpWindow is how long the owners have to open a retrospective's
// follow-ups with "create" before autopilot closes the milestone.
const FollowUpWindow = 7 * 24 * time.Hour

// FollowUpsSettled reports whether the retrospective's follow-ups no longer
// need the milestone: there are none, an owner dismissed them, all were
// opened or taken over by another milestone, or the window to open them
// is over.
func (r *PilotRetrospective) FollowUpsSettled(now time.Time) bool {
	if r.Failed != "" || r.DismissedAt != nil || len(r.FollowUps) == 0 || r.PostedAt == nil || !now.Before(r.PostedAt.Add(FollowUpWindow)) {
		return true
	}
	for k := range r.FollowUps {
		key := strconv.Itoa(k + 1)
		_, created := r.Created[key]
		if _, taken := r.Taken[key]; !created && !taken {
			return false
		}
	}
	return true
}

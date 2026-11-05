package v1alpha1

// QueueLess reports whether task a goes ahead of task b in its project's run
// queue: the urgent lane (the fix of a red default branch) first, then tasks
// without a milestone, then the lowest milestone iid, which is the oldest
// milestone. Within a rank the older task goes first, then the name, so the
// order is total.
func QueueLess(a, b *AgentTask) bool {
	if ua, ub := a.Spec.QueueLane == QueueLaneUrgent, b.Spec.QueueLane == QueueLaneUrgent; ua != ub {
		return ua
	}
	ma, mb := a.Spec.Milestone, b.Spec.Milestone
	switch {
	case ma == nil && mb != nil:
		return true
	case ma != nil && mb == nil:
		return false
	case ma != nil && ma.IID != mb.IID:
		return ma.IID < mb.IID
	}
	if ta, tb := a.CreationTimestamp, b.CreationTimestamp; !ta.Equal(&tb) {
		return ta.Before(&tb)
	}
	return a.Name < b.Name
}

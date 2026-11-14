package controller

const (
	// LabelProject is set on every AgentTask, Pod and PVC created for a project.
	LabelProject = "ai.wiemers.me/project"
	// LabelTask is set on the pod and PVC of a task.
	LabelTask = "ai.wiemers.me/task"
	// LabelType is the task type (spec / implement / coordinate).
	LabelType = "ai.wiemers.me/type"
	// LabelMilestoneIID is set on spec and coordinate tasks.
	LabelMilestoneIID = "ai.wiemers.me/milestone-iid"
	// LabelIssueIID is set on implement tasks. Tasks are looked up by IID, not
	// by name, so renaming an issue does not create a second task.
	LabelIssueIID = "ai.wiemers.me/issue-iid"
	// LabelPlan is set on the pod and PVC of an AgentPlan.
	LabelPlan = "ai.wiemers.me/plan"
)

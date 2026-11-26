package v1alpha1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentProjectSpec connects one GitLab project to the operator and describes
// how its milestones and issues are turned into agent work.
type AgentProjectSpec struct {
	// GitLab connection settings.
	GitLab GitLabSpec `json:"gitlab"`

	// Milestones selects which milestones get a spec agent.
	// +optional
	Milestones MilestoneSelector `json:"milestones,omitempty"`

	// Workflow defines the labels that drive the issue state machine.
	// +optional
	Workflow WorkflowSpec `json:"workflow,omitempty"`

	// Agents configures the agent pods and the individual agent roles.
	Agents AgentsSpec `json:"agents"`

	// Lifecycle controls how long agent pods and workspaces are kept around.
	// +optional
	Lifecycle LifecycleSpec `json:"lifecycle,omitempty"`

	// CI configures how the agent reacts to the merge request pipeline.
	// +optional
	CI CISpec `json:"ci,omitempty"`

	// MainHealth watches the pipelines of the default branch.
	// +optional
	MainHealth MainHealthSpec `json:"mainHealth,omitempty"`

	// LooseIssues configures the project's queue of issues without a
	// milestone that owners hand to autopilot.
	// +optional
	LooseIssues LooseIssuesSpec `json:"looseIssues,omitzero"`

	// Checkpoint configures the checkpoint commits that save the work tree
	// of an implementer call that was cut off (usage limit, engine error,
	// timeout, budget stop). On unless disabled.
	// +optional
	Checkpoint CheckpointSpec `json:"checkpoint,omitempty"`

	// Notify says whom the operator mentions when it needs a human.
	// +optional
	Notify NotifySpec `json:"notify,omitempty"`

	// Pilot configures the milestone coordinator (see agents.pilot).
	// +optional
	Pilot PilotSpec `json:"pilot,omitempty"`

	// Webhook configures the deliveries of the GitLab project hook.
	// +optional
	Webhook WebhookSpec `json:"webhook,omitempty"`

	// Research configures the AgentResearch runs that reference this
	// project (see docs/guides/research.md).
	// +optional
	Research *ProjectResearch `json:"research,omitempty"`

	// Budget limits what autopilot work may spend (agent_calls, needs the
	// research database).
	// +optional
	Budget *BudgetSpec `json:"budget,omitempty"`

	// CrossRepo lets the project's issues change a companion repository in
	// the same run (see docs/specs/cross-repo-changes.spec.md). Needs the
	// cluster's allow-list (--cross-repo-allowed-projects).
	// +optional
	CrossRepo *CrossRepoSpec `json:"crossRepo,omitempty"`

	// Autopilot is the project's autopilot settings.
	// +optional
	Autopilot *AutopilotSpec `json:"autopilot,omitempty"`

	// HouseRules are the rules the Desk planner writes into a milestone's
	// Constraints and the planning agent sizes issues by.
	// +optional
	HouseRules *HouseRulesSpec `json:"houseRules,omitempty"`

	// PollInterval is how often GitLab is polled for changes.
	// +kubebuilder:default="1m"
	// +optional
	PollInterval metav1.Duration `json:"pollInterval,omitempty"`

	// MaxConcurrentRuns limits how many agent runs (i.e. token-consuming work)
	// may be in flight for this project at the same time. Further runs queue.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxConcurrentRuns int32 `json:"maxConcurrentRuns,omitempty"`

	// Suspend stops polling GitLab and starting new runs. Existing pods are
	// left alone and are still subject to the lifecycle timeouts.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// GitLabSpec describes how to reach the GitLab project.
type GitLabSpec struct {
	// URL of the GitLab instance, e.g. https://gitlab.com.
	// +kubebuilder:default="https://gitlab.com"
	// +optional
	URL string `json:"url,omitempty"`

	// Project is the full path of the project, e.g. "my-group/my-project".
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`

	// TokenSecretRef points to a project/group access token with the api and
	// write_repository scopes. The token is used by the operator and by the
	// runner inside the agent pods; it is never handed to the model.
	TokenSecretRef corev1.SecretKeySelector `json:"tokenSecretRef"`
}

// BudgetSpec is the project's budgets (a milestone's own is its
// description's "/agent budget <usd>" line).
type BudgetSpec struct {
	// PerIssueUSD is the budget of every issue of an autopilot milestone, as
	// a decimal string, e.g. "20": once an issue's calls cost that much,
	// autopilot pauses. An "/agent budget <usd>" comment on the issue
	// overrides it. Unset or 0: no per-issue budget.
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +optional
	PerIssueUSD string `json:"perIssueUSD,omitempty"`

	// PerIssueAlertUSD is the spend of an issue of an autopilot milestone,
	// as a decimal string, e.g. "15", at which autopilot posts one heads-up
	// on the tracking issue (and wakes the pilot) without pausing: the
	// issue's spend and its top kinds and models. Once per issue and value;
	// another value re-arms it. The same heads-up comes at 80 % of an
	// issue's own budget. Unset or 0: no alert.
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +optional
	PerIssueAlertUSD string `json:"perIssueAlertUSD,omitempty"`
}

// AutopilotSpec is the project's autopilot settings.
type AutopilotSpec struct {
	// MaxParallel is how many issues of an autopilot milestone are
	// implemented at the same time. 1 is one at a time. A milestone's
	// `/agent parallel N` line overrides it.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxParallel int32 `json:"maxParallel,omitempty"`
}

// HouseRulesSpec is the project's rules for milestones and their merge
// requests.
type HouseRulesSpec struct {
	// MaxMRLines is the size budget of one merge request in production
	// lines; tests, generated files and docs do not count. Unset is 300,
	// 0 states no budget.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxMRLines *int32 `json:"maxMRLines,omitempty"`

	// Constraints are further rules, one line each, that every merge
	// request of the project follows.
	// +optional
	Constraints []string `json:"constraints,omitempty"`
}

// MilestoneSelector selects milestones that should be planned by the spec agent.
type MilestoneSelector struct {
	// TitlePattern is an optional regular expression milestone titles must match.
	// +optional
	TitlePattern string `json:"titlePattern,omitempty"`

	// SpecMarker must appear in the milestone description for it to be picked
	// up. This keeps existing milestones from being planned by accident. Set it
	// to an empty string to plan every active milestone with a description.
	// +kubebuilder:default="/agent spec"
	// +optional
	SpecMarker *string `json:"specMarker,omitempty"`

	// AutopilotMarker in a milestone description plans the milestone (like
	// SpecMarker) and runs it end to end: the agents take their defaults
	// instead of asking, the issues are worked through one at a time in
	// dependency order, and each merge request is merged once its pipeline
	// is green and its review is clean. The bot needs permission to merge
	// into the default branch. Removing the marker stops it.
	// +kubebuilder:default="/agent autopilot"
	// +optional
	AutopilotMarker *string `json:"autopilotMarker,omitempty"`
}

// InputSource is where an owner may give the agents instructions.
// +kubebuilder:validation:Enum=gitlab;desk
type InputSource string

const (
	// InputGitLab: "/agent" and "/pilot" comments on GitLab.
	InputGitLab InputSource = "gitlab"
	// InputDesk: the Desk (prompts, chat).
	InputDesk InputSource = "desk"
)

// WorkflowSpec defines the GitLab labels used as the agent state machine. Use
// scoped labels (with "::") so GitLab keeps exactly one of them on an issue and
// they map nicely onto board columns.
type WorkflowSpec struct {
	// DraftLabel is put on issues created by the spec agent. Draft issues are
	// not worked on until a human moves them to ReadyLabel.
	// +kubebuilder:default="agent::draft"
	// +optional
	DraftLabel string `json:"draftLabel,omitempty"`

	// ReadyLabel is set by a human to hand an issue to the implementing agent.
	// +kubebuilder:default="agent::ready"
	// +optional
	ReadyLabel string `json:"readyLabel,omitempty"`

	// WorkingLabel is set by the operator once it has picked up a ready issue.
	// +kubebuilder:default="agent::working"
	// +optional
	WorkingLabel string `json:"workingLabel,omitempty"`

	// NeedsInputLabel is set by the agent when it has questions it cannot
	// resolve on its own.
	// +kubebuilder:default="agent::needs-input"
	// +optional
	NeedsInputLabel string `json:"needsInputLabel,omitempty"`

	// ReviewLabel is set by the agent once a merge request is ready for a human.
	// +kubebuilder:default="agent::review"
	// +optional
	ReviewLabel string `json:"reviewLabel,omitempty"`

	// BlockedLabel is set by the operator when an issue cannot go on for a
	// reason outside it (an outage, a red default branch); the label it
	// replaced comes back once the block is lifted.
	// +kubebuilder:default="agent::blocked"
	// +optional
	BlockedLabel string `json:"blockedLabel,omitempty"`

	// BlockedReasonPrefix: a blocked issue also carries
	// "<prefix><reason>", e.g. "agent-blocked::infra".
	// +kubebuilder:default="agent-blocked::"
	// +optional
	BlockedReasonPrefix string `json:"blockedReasonPrefix,omitempty"`

	// DoneLabel (or closing the issue) ends the task and deletes its pod and
	// workspace.
	// +kubebuilder:default="agent::done"
	// +optional
	DoneLabel string `json:"doneLabel,omitempty"`

	// GeneratedLabel is added to every issue the spec agent creates.
	// +kubebuilder:default="ai-generated"
	// +optional
	GeneratedLabel string `json:"generatedLabel,omitempty"`

	// ProfileLabelPrefix: an issue labelled "<prefix><profile name>" is
	// implemented with that agent profile (see agents.profiles). The spec agent
	// sets one on every issue it creates; change the label to override its
	// choice. Issues without a profile label use agents.implement.
	// +kubebuilder:default="agent-profile::"
	// +optional
	ProfileLabelPrefix string `json:"profileLabelPrefix,omitempty"`

	// ModelLabelPrefix: an issue labelled "<prefix><engine>/<model or
	// alias>" (e.g. "agent-model::claude/opus") is implemented with that
	// engine and model instead of its profile's, if agents.models allows it.
	// "/agent use <engine> <model>" sets it.
	// +kubebuilder:default="agent-model::"
	// +optional
	ModelLabelPrefix string `json:"modelLabelPrefix,omitempty"`

	// EffortLabelPrefix: an issue labelled "<prefix><level>" (e.g.
	// "agent-effort::high") is implemented with that effort, at most
	// agents.maxEffort (claude engine only), instead of its profile's. The
	// spec agent sets none: it picks a profile; "/agent effort <level>" sets
	// one.
	// +kubebuilder:default="agent-effort::"
	// +optional
	EffortLabelPrefix string `json:"effortLabelPrefix,omitempty"`

	// CommandPrefix: a comment on the issue or its merge request that starts
	// with this prefix triggers another agent run (e.g. "/agent please also
	// cover the error case"). Plain discussion does not trigger runs, so no
	// tokens are spent on chatter.
	// +kubebuilder:default="/agent"
	// +optional
	CommandPrefix string `json:"commandPrefix,omitempty"`

	// PilotPrefix: a comment that starts with this prefix talks to the
	// milestone's pilot (e.g. "/pilot why are we paused?").
	// +kubebuilder:default="/pilot"
	// +optional
	PilotPrefix string `json:"pilotPrefix,omitempty"`

	// CommandsFrom is who may give CommandPrefix commands on issues, merge
	// requests and tracking issues: "members" (anyone who can comment) or
	// "owners" (the milestone's owners; for an issue without a milestone,
	// notify.users, else the operator's default owners). Others get one
	// reply a day.
	// +kubebuilder:default=members
	// +optional
	CommandsFrom CommandsFrom `json:"commandsFrom,omitempty"`

	// Inputs are the places that take instructions: "gitlab" (comment
	// commands) and "desk" (prompts and chat in the Desk). Unset means both.
	// Labels, milestone planning and human merge request review threads stay
	// on GitLab either way. Without "gitlab", "/agent" and "/pilot" comments
	// are not acted on: each gets one reply pointing to the Desk, and the
	// implementer reads no issue thread: a fresh session gets the Desk chat
	// (spec.source.chatContext of its task) instead.
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	// +optional
	Inputs []InputSource `json:"inputs,omitempty"`

	// AssignBot assigns the bot user to an issue when the operator picks it up
	// and the issue has no assignee, so GitLab's milestone page lists it as
	// ongoing. A human assignee is never replaced.
	// +kubebuilder:default=true
	// +optional
	AssignBot *bool `json:"assignBot,omitempty"`

	// ResolveConflicts lets the agent resolve merge conflicts: when the
	// merge request of an issue in review conflicts with the default branch,
	// the next run merges the default branch into the work branch and the
	// agent resolves the conflicts. The result is reviewed and checked by CI
	// like any push. Off: conflicts are left to humans (autopilot pauses).
	// +kubebuilder:default=true
	// +optional
	ResolveConflicts *bool `json:"resolveConflicts,omitempty"`

	// StepCommits: while the merge request is a draft, the implementer works
	// in small steps; each one is committed and pushed (without CI) as it is
	// done, and comments written in the meantime reach the agent before the
	// next step, so a human can follow and steer. Off: one commit per run.
	// +kubebuilder:default=true
	// +optional
	StepCommits *bool `json:"stepCommits,omitempty"`

	// ConventionalCommits: commit messages and merge request titles follow
	// Conventional Commits (type(scope): subject). Off keeps today's messages
	// and titles.
	// +kubebuilder:default=false
	// +optional
	ConventionalCommits *bool `json:"conventionalCommits,omitempty"`

	// CommitTypes are the allowed Conventional Commits types. Default: feat,
	// fix, refactor, perf, test, docs, build, ci, chore, style, revert. chore
	// is always allowed (the runner's own messages use it); the defaults add
	// it to a list that lacks it.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9]*$`
	// +optional
	CommitTypes []string `json:"commitTypes,omitempty"`

	// Release configures when the operator releases the default branch by
	// decision instead of on every merge, see docs/guides/releases.md.
	// +optional
	Release ReleaseSpec `json:"release,omitempty"`
}

// ReleaseMode is whether the operator starts release pipelines.
// +kubebuilder:validation:Enum=Auto;Off
type ReleaseMode string

const (
	// ReleaseAuto: the operator releases by decision. The default, also when
	// the mode is empty.
	ReleaseAuto ReleaseMode = "Auto"
	// ReleaseOff: the operator never starts a release pipeline.
	ReleaseOff ReleaseMode = "Off"
)

// ReleaseSpec configures releasing by decision, see docs/guides/releases.md.
type ReleaseSpec struct {
	// Mode: Auto (default, also when empty) or Off. Off means the operator
	// never starts a release pipeline.
	// +kubebuilder:default=Auto
	// +optional
	Mode ReleaseMode `json:"mode,omitempty"`

	// MinInterval is the minimum time between two releases the operator
	// starts. The final release of a milestone is exempt.
	// +kubebuilder:default="15m"
	// +optional
	MinInterval *metav1.Duration `json:"minInterval,omitempty"`

	// MaxUnreleasedAge: outside milestones, release-relevant commits on the
	// default branch that are older than this are released.
	// +kubebuilder:default="24h"
	// +optional
	MaxUnreleasedAge *metav1.Duration `json:"maxUnreleasedAge,omitempty"`
}

// Auto reports whether the operator releases by decision: true for Auto and
// for an empty mode.
func (s ReleaseSpec) Auto() bool { return s.Mode != ReleaseOff }

// ReviewSpec configures the review loop that runs after each implementation.
type ReviewSpec struct {
	RoleSpec `json:",inline" tstype:",extends"`

	// Reviewers to run. Each reviewer is a separate, read-only agent that only
	// gets the diff plus the issue as input. They run in parallel.
	// +kubebuilder:default={bugbot,security}
	// +optional
	Reviewers []ReviewerKind `json:"reviewers,omitempty"`

	// MaxIterations is how often the implementer may fix blocking review
	// findings before the result is handed to a human anyway.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxIterations *int32 `json:"maxIterations,omitempty"`

	// Retries is how often a reviewer is called again after a transient
	// failure (an outage of the engine's service, a reset connection), with
	// a growing pause in between. Other failures are not retried.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +optional
	Retries *int32 `json:"retries,omitempty"`

	// RetryAfter is how long autopilot waits before it asks for one
	// re-review of a head whose reviewers all failed transiently.
	// +kubebuilder:default="15m"
	// +optional
	RetryAfter metav1.Duration `json:"retryAfter,omitempty"`

	// ReviewerSettings tunes single reviewers. A reviewer without an entry
	// uses the defaults.
	// +listType=map
	// +listMapKey=name
	// +optional
	ReviewerSettings []ReviewerSettings `json:"reviewerSettings,omitempty"`
}

// ReviewerSettings are the settings of one reviewer.
type ReviewerSettings struct {
	// Name of the reviewer these settings apply to.
	Name ReviewerKind `json:"name"`

	// MaxInlineDiff is the largest diff, in characters, that is pasted into
	// this reviewer's prompt each round; a larger diff is referenced and the
	// reviewer reads it through git. Unset: 150000.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxInlineDiff *int32 `json:"maxInlineDiff,omitempty"`
}

// DefaultMaxInlineDiff is the default for ReviewerSettings.MaxInlineDiff.
const DefaultMaxInlineDiff = 150_000

// MaxInlineDiff returns the inline diff cap in characters for a reviewer.
func (r ReviewSpec) MaxInlineDiff(kind ReviewerKind) int {
	for _, s := range r.ReviewerSettings {
		if s.Name == kind && s.MaxInlineDiff != nil {
			return int(*s.MaxInlineDiff)
		}
	}
	return DefaultMaxInlineDiff
}

// ReviewerKind names a built-in reviewer.
// +kubebuilder:validation:Enum=bugbot;security
type ReviewerKind string

const (
	ReviewerBugbot   ReviewerKind = "bugbot"
	ReviewerSecurity ReviewerKind = "security"
)

// LifecycleSpec controls how long pods and workspaces live.
//
// A task goes through: Running -> Idle (briefly, for follow-up requests) ->
// Hibernated (pod deleted, workspace PVC kept) -> Archived (PVC deleted; the branch and the
// GitLab history remain, so the next run rehydrates from a fresh clone).
type LifecycleSpec struct {
	// IdleTimeout after which a pod without pending runs is deleted while its
	// workspace is kept. The short default stops the pod right after a run;
	// a request that arrives during the run or within the grace starts in the
	// same pod. Everything the next run needs stays on the workspace, so a
	// longer timeout only saves the pod start (scheduling, volume attach,
	// image pull if needed) at the cost of holding the pod's resources. After
	// a failed run the pod stays at least an hour, so its logs can be read.
	// +kubebuilder:default="1m"
	// +optional
	IdleTimeout metav1.Duration `json:"idleTimeout,omitempty"`

	// ChatIdleTimeout after the last Desk chat turn before an idle pod is
	// hibernated. Longer than idleTimeout so a conversation can continue
	// without a cold start; pods without a recent chat turn still use
	// idleTimeout.
	// +kubebuilder:default="15m"
	// +optional
	ChatIdleTimeout metav1.Duration `json:"chatIdleTimeout,omitempty"`

	// ProblemWarmMax caps how long the operator keeps a milestone's pilot
	// pod up while a problem waits for an owner, counted from when the
	// problem opened. An idle pod costs no tokens, only its resources.
	// +kubebuilder:default="2h"
	// +optional
	ProblemWarmMax metav1.Duration `json:"problemWarmMax,omitempty"`

	// RetentionPeriod after the last activity after which the workspace PVC is
	// deleted as well.
	// +kubebuilder:default="168h"
	// +optional
	RetentionPeriod metav1.Duration `json:"retentionPeriod,omitempty"`

	// MaxRunsPerTask is a safety net against runaway loops.
	// +kubebuilder:default=20
	// +optional
	MaxRunsPerTask int32 `json:"maxRunsPerTask,omitempty"`

	// UsageLimitBackoff is the first wait after an engine reports a usage or
	// rate limit without saying when it resets. It doubles with every
	// further deferral of the same run, up to MaxUsageLimitBackoff.
	// +kubebuilder:default="5m"
	// +optional
	UsageLimitBackoff metav1.Duration `json:"usageLimitBackoff,omitempty"`

	// MaxUsageLimitBackoff caps the wait between retries.
	// +kubebuilder:default="1h"
	// +optional
	MaxUsageLimitBackoff metav1.Duration `json:"maxUsageLimitBackoff,omitempty"`

	// InfraRetryAfter is the wait before the operator starts a run that
	// failed for an outside reason (the issue is blocked, infra; for planning
	// also a transient GitLab error) once more.
	// +kubebuilder:default="30m"
	// +optional
	InfraRetryAfter metav1.Duration `json:"infraRetryAfter,omitempty"`

	// StallAfter: a Running task whose runner and agent containers both
	// logged nothing for this long, or whose pod is Pending or crash-/
	// image-pull-looping this long, is stalled: the operator sets condition
	// Stalled on the task, which stays Running.
	// +kubebuilder:default="30m"
	// +optional
	StallAfter metav1.Duration `json:"stallAfter,omitempty"`

	// StallCheckInterval is how often the operator checks a Running task
	// for a stall. Tasks shorter than StallAfter are never looked at.
	// +kubebuilder:default="5m"
	// +optional
	StallCheckInterval metav1.Duration `json:"stallCheckInterval,omitempty"`
}

// CISpec configures the CI feedback loop: after the agent pushes, the
// operator waits for the merge request pipeline. A green pipeline moves the
// issue to review; a red one is retried once and then handed back to the
// implementer with the failing jobs, up to MaxFixRounds times.
type CISpec struct {
	// Disabled turns the loop off: issues go to review right after the push.
	// +optional
	Disabled bool `json:"disabled,omitempty"`

	// MaxFixRounds is how many times the agent may try to fix a red pipeline
	// after a push triggered by a human, before the issue goes to
	// needs-input.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxFixRounds *int32 `json:"maxFixRounds,omitempty"`

	// RetryFailedJobs retries the failed jobs of a red pipeline once before
	// the agent spends tokens on it, to weed out flaky jobs.
	// +kubebuilder:default=true
	// +optional
	RetryFailedJobs *bool `json:"retryFailedJobs,omitempty"`

	// NoPipelineTimeout: if no pipeline shows up for the pushed commit
	// within this time, the project is assumed to have no CI for it and the
	// issue goes to review.
	// +kubebuilder:default="10m"
	// +optional
	NoPipelineTimeout metav1.Duration `json:"noPipelineTimeout,omitempty"`
}

// CheckpointSpec configures checkpoints: when an implementer call is cut
// off, the runner commits the work tree on the work branch as
// "chore(wip): checkpoint #<issue> step <n> (<reason>)" and pushes it
// without CI, so the next run continues from it.
type CheckpointSpec struct {
	// Disabled turns checkpoints off: the work of a call that was cut off
	// is reset with the workspace, as before.
	// +optional
	Disabled bool `json:"disabled,omitempty"`

	// MaxFileBytes is the largest file a checkpoint commits; bigger ones
	// stay in the workspace and are named in the run log. Default 5 MiB.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxFileBytes *int64 `json:"maxFileBytes,omitempty"`
}

// NotifyEvent is a situation in which the operator needs a human.
// +kubebuilder:validation:Enum=failed;needsInput;ciFailed;review;autopilotPaused;autopilotDone;mainRed;blocked
type NotifyEvent string

const (
	// NotifyFailed: a run failed.
	NotifyFailed NotifyEvent = "failed"
	// NotifyNeedsInput: the agent asked questions or could not finish alone.
	NotifyNeedsInput NotifyEvent = "needsInput"
	// NotifyCIFailed: the pipeline stayed red after the fix rounds.
	NotifyCIFailed NotifyEvent = "ciFailed"
	// NotifyReview: an issue reached review (not on autopilot).
	NotifyReview NotifyEvent = "review"
	// NotifyAutopilotPaused: autopilot paused a milestone.
	NotifyAutopilotPaused NotifyEvent = "autopilotPaused"
	// NotifyAutopilotDone: autopilot finished a milestone.
	NotifyAutopilotDone NotifyEvent = "autopilotDone"
	// NotifyMainRed: the default branch stayed red after a retry.
	NotifyMainRed NotifyEvent = "mainRed"
	// NotifyBlocked: a run gave up for an outside reason; the issue is
	// blocked and waits for a human.
	NotifyBlocked NotifyEvent = "blocked"
)

// NotifySpec says whom the operator mentions ("cc @…") in the comments that
// need a human. A milestone overrides the users with a line
// "<commandPrefix> owners @alice @bob" in its description, for itself and
// its issues.
type NotifySpec struct {
	// Users are the GitLab usernames (without "@") to mention. Empty: the
	// operator's --default-owners. They must be members of the project to
	// be notified.
	// +optional
	Users []string `json:"users,omitempty"`

	// Events that mention the users. Defaults to all of them.
	// +optional
	Events []NotifyEvent `json:"events,omitempty"`

	// RequestReview makes the users the reviewers of a merge request whose
	// issue reaches review (not on autopilot), unless it has reviewers.
	// +kubebuilder:default=true
	// +optional
	RequestReview *bool `json:"requestReview,omitempty"`
}

// PilotSpec configures the milestone coordinator ("/pilot").
type PilotSpec struct {
	// Mention: a comment that starts with a mention of the bot user talks
	// to the pilot too, like workflow.pilotPrefix.
	// +kubebuilder:default=true
	// +optional
	Mention *bool `json:"mention,omitempty"`

	// MaxBudgetUSD caps the pilot's spend per milestone, as a decimal
	// string. Once it is used up, the pilot answers that it is, without a
	// model call.
	// +kubebuilder:default="20"
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +optional
	MaxBudgetUSD string `json:"maxBudgetUSD,omitempty"`

	// Autonomy: managed, the pilot retries, re-reviews, instructs (a fixed
	// kind) and opens issues on its own, counted and audited on the
	// tracking issue; propose, every action waits for an owner's "yes".
	// Unset: managed on autopilot milestones, propose on the others; a set
	// value applies to every milestone. Neither a milestone's description
	// nor its rules change it; an owner's "/pilot stop" lowers it.
	// +kubebuilder:validation:Enum=managed;propose
	// +optional
	Autonomy PilotAutonomy `json:"autonomy,omitempty"`

	// MaxInterventionsPerIssue bounds the pilot's own actions per issue
	// and head when it manages; past it, it proposes.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxInterventionsPerIssue int32 `json:"maxInterventionsPerIssue,omitempty"`

	// StuckAfter: an autopilot pause whose state (items, CI states, main
	// health, MR heads) has not changed for this long is assessed by the
	// pilot, once per pause. The owners get its options as a prompt; it
	// never resumes on its own.
	// +kubebuilder:default="30m"
	// +optional
	StuckAfter *metav1.Duration `json:"stuckAfter,omitempty"`
}

// StuckAfterOf is StuckAfter, DefaultPilotStuckAfter when unset or not
// positive (an object written before the field existed).
func (p PilotSpec) StuckAfterOf() time.Duration {
	if p.StuckAfter == nil || p.StuckAfter.Duration <= 0 {
		return DefaultPilotStuckAfter
	}
	return p.StuckAfter.Duration
}

// PilotAutonomy is how far the pilot acts without an owner's "yes".
type PilotAutonomy string

const (
	PilotManaged PilotAutonomy = "managed"
	PilotPropose PilotAutonomy = "propose"
)

// AgentProjectStatus is the observed state of an AgentProject.
type AgentProjectStatus struct {
	// LastSyncTime is when GitLab was last polled successfully.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// BotUserID is the GitLab user behind the token. Its comments never
	// trigger runs.
	// +optional
	BotUserID int64 `json:"botUserID,omitempty"`

	// BotUsername is the username of BotUserID, read on every poll, so a
	// renamed bot is still recognized in mentions.
	// +optional
	BotUsername string `json:"botUsername,omitempty"`

	// BotGitName is the git author and committer name of the operator's
	// commits: the token user's name, or its username when the name is
	// empty. Read with BotUsername.
	// +optional
	BotGitName string `json:"botGitName,omitempty"`

	// BotGitEmail is the git author and committer email of the operator's
	// commits: the token user's commit email, or else its GitLab noreply
	// address <id>-<username>@users.noreply.gitlab.com.
	// +optional
	BotGitEmail string `json:"botGitEmail,omitempty"`

	// ActiveMilestones is the number of tracked milestones that are not
	// settling (see settlingMilestones).
	// +optional
	ActiveMilestones int32 `json:"activeMilestones,omitempty"`

	// SettlingMilestones is the number of milestones whose autopilot is done
	// and that only wait for their retrospective's follow-ups.
	// +optional
	SettlingMilestones int32 `json:"settlingMilestones,omitempty"`

	// MilestoneOrder is the open autopilot milestones' iids in run order;
	// the first may start new issues. Each sync seeds it once from the
	// run-next marks, drops milestones that are done, closed or off
	// autopilot and appends new ones by iid (internal/milestoneorder).
	// +optional
	// +listType=atomic
	MilestoneOrder []int64 `json:"milestoneOrder,omitempty"`

	// ActiveTasks is the number of AgentTasks belonging to this project.
	// +optional
	ActiveTasks int32 `json:"activeTasks,omitempty"`

	// UsageLimits lists engines that currently hit a usage or rate limit.
	// Runs that need such an engine wait instead of hitting the limit again;
	// limits belong to the credentials, so they apply to the whole project.
	// +optional
	UsageLimits []UsageLimit `json:"usageLimits,omitempty"`

	// EngineUsage is the freshest usage reading per engine of the project's
	// tasks: how much of each usage window is used and when it resets.
	// Readings outlive their tasks until a newer one arrives.
	// +optional
	EngineUsage []EngineUsage `json:"engineUsage,omitempty"`

	// MainHealth is the health of the default branch.
	// +optional
	MainHealth *MainHealthStatus `json:"mainHealth,omitempty"`

	// Webhook is the project hook the operator registered and the last
	// delivery.
	// +optional
	Webhook *WebhookStatus `json:"webhook,omitempty"`

	// CommandReplies tracks the replies to "/agent" and "/pilot" comments
	// the operator does not act on, so each gets one.
	// +optional
	CommandReplies *CommandRepliesStatus `json:"commandReplies,omitempty"`

	// Companions tracks the companion merge requests whose home merge
	// request was merged, until they land: one entry per companion merge
	// request (project and mrIID), kept for CompanionRetention after it
	// ended. The AgentTask of the home issue may be gone by then.
	// +optional
	// +listType=atomic
	Companions []CompanionStatus `json:"companions,omitempty"`

	// Releases are release decisions outside milestones (sweep, companion),
	// newest 20.
	// +optional
	Releases []ReleaseRecord `json:"releases,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MaxCommandReplies bounds CommandRepliesStatus.Replied.
const MaxCommandReplies = 200

// CommandRepliesStatus is the watermark of the comments answered with why
// the operator does not act on them.
type CommandRepliesStatus struct {
	// Since is the start of the last scan; older comments were scanned.
	// +optional
	Since *metav1.Time `json:"since,omitempty"`
	// Replied are the IDs of the comments answered, newest last
	// (MaxCommandReplies at most). The reply's marker covers older ones.
	// +optional
	Replied []int64 `json:"replied,omitempty"`
	// Authors counts the replies per author, by GitLab user ID, within a
	// day of the first; an author's comments beyond the cap get none.
	// +optional
	Authors map[string]CommandReplyCount `json:"authors,omitempty"`
}

// CommandReplyCount is the replies to one author's comments within a day.
type CommandReplyCount struct {
	// Since is the first reply of the day.
	Since metav1.Time `json:"since"`
	Count int32       `json:"count"`
}

// UsageLimit is an engine's active usage or rate limit.
type UsageLimit struct {
	Engine Engine      `json:"engine"`
	Until  metav1.Time `json:"until"`
	// Message reported by the engine.
	// +optional
	Message string `json:"message,omitempty"`
	// Deferred marks, in a task's status.run.usageLimits, the limit that
	// deferred the run (see status.run.deferredUntil for the retry time).
	// +optional
	Deferred bool `json:"deferred,omitempty"`
}

// EngineUsage is what an engine last said about its usage windows (claude
// reports them with every call; cursor reports none).
type EngineUsage struct {
	Engine Engine `json:"engine"`

	// Status as the engine reports it, e.g. allowed, allowed_warning or
	// rejected.
	// +optional
	Status string `json:"status,omitempty"`

	// Windows are the engine's usage windows, e.g. five_hour and seven_day.
	// +optional
	Windows []UsageWindow `json:"windows,omitempty"`

	// ObservedAt is when the engine reported it.
	ObservedAt metav1.Time `json:"observedAt"`
}

// UsageWindow is how much of one usage window is used.
type UsageWindow struct {
	Name string `json:"name"`

	// Percent of the window that is used.
	Percent int32 `json:"percent"`

	// ResetsAt is when the window starts over.
	// +optional
	ResetsAt *metav1.Time `json:"resetsAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ap
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.gitlab.project`
// +kubebuilder:printcolumn:name="Tasks",type=integer,JSONPath=`.status.activeTasks`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentProject connects a GitLab project to the operator.
type AgentProject struct {
	metav1.TypeMeta   `json:",inline" tstype:",extends"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentProjectSpec   `json:"spec,omitempty"`
	Status AgentProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentProjectList contains a list of AgentProject.
type AgentProjectList struct {
	metav1.TypeMeta `json:",inline" tstype:",extends"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentProject `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentProject{}, &AgentProjectList{})
}

// Mirrors the views of internal/ui/view.go and the status types of
// api/v1alpha1.

export type Stage =
  | 'pending'
  | 'working'
  | 'waiting'
  | 'needs-input'
  | 'ci'
  | 'review'
  | 'planned'
  | 'failed'
  | 'blocked'

export interface Me {
  username: string
  roles: string[] | null
  authMode: 'oidc' | 'none'
}

export interface Condition {
  type: string
  status: 'True' | 'False' | 'Unknown'
  reason?: string
  message?: string
  lastTransitionTime?: string
}

export interface UsageLimit {
  engine: string
  until: string
  message?: string
}

export interface UsageWindow {
  name: string
  percent: number
  resetsAt?: string
}

// EngineUsage is what an engine last said about its usage windows (claude only).
export interface EngineUsage {
  engine: string
  status?: string
  windows?: UsageWindow[]
  observedAt: string
}

export interface AutopilotOverview {
  milestoneIID: number
  title: string
  phase: AutopilotPhase
  pausedReason?: string
  // waitingFor: a run is working on this paused issue; autopilot continues by itself.
  waitingFor?: number
  done: number
  total: number
}

export interface ProjectSummary {
  namespace: string
  name: string
  gitlab: { url: string; project: string; webURL: string }
  suspend: boolean
  maxConcurrentRuns: number
  pollInterval: string
  lastSyncTime?: string
  usageLimits?: UsageLimit[]
  engineUsage?: EngineUsage[]
  conditions?: Condition[]
  tasks: number
  stages: Partial<Record<Stage, number>>
  costUSD: string
  createdAt: string
  autopilots?: AutopilotOverview[]
}

export interface ProjectDetail extends ProjectSummary {
  milestones: Milestone[]
  unplanned: Task[]
}

export interface Milestone {
  id: number
  iid: number
  title: string
  webURL: string
  spec?: Task
  // trackingIssueIID: the milestone's tracking issue (its chat), if any.
  trackingIssueIID?: number
  tasks: Task[]
  costUSD: string
}

export interface MilestoneRef {
  id: number
  iid: number
  title: string
}

export interface IssueRef {
  id: number
  iid: number
  title: string
  webURL?: string
}

export type TaskPhase =
  | 'Pending'
  | 'Queued'
  | 'Running'
  | 'Waiting'
  | 'Idle'
  | 'Hibernated'
  | 'Archived'
  | 'Blocked'

export type CIState =
  | 'Pending'
  | 'Running'
  | 'Fixing'
  | 'Success'
  | 'Failed'
  | 'BaseFailing'
  | 'Canceled'
  | 'None'

export interface CIStatus {
  sha: string
  state: CIState
  pipelineID?: number
  pipelineURL?: string
  retried?: boolean
  failedJobs?: string[]
}

export interface RunStatus {
  activeRunRequest?: number
  observedRunRequest?: number
  currentStep?: string
  runCount?: number
  lastActivityTime?: string
  lastResult?: 'Succeeded' | 'NeedsInput' | 'Failed' | 'Deferred'
  summary?: string
  costUSD?: string
  branch?: string
  mergeRequestIID?: number
  mergeRequestURL?: string
  deferredUntil?: string
  limitedEngine?: string
  deferrals?: number
  oomRetries?: number
  pushedSHA?: string
  pushedAt?: string
  ciRound?: number
  blockingFindings?: number
  reviewErrors?: number
  reviewedSHA?: string
}

export interface ContainerTermination {
  reason?: string
  exitCode?: number
  finishedAt?: string
  restarts?: number
  memoryLimit?: string
}

export interface PlannedIssue {
  key: string
  iid: number
  title?: string
  dependsOn?: number[]
}

export type AutopilotPhase = 'Running' | 'Paused' | 'Done' | 'Off'

// PauseKind mirrors api/v1alpha1.PauseKind: why autopilot paused.
export type PauseKind =
  | 'owner'
  | 'cycle'
  | 'stuck'
  | 'needsInput'
  | 'runFailed'
  | 'findings'
  | 'reviewErrors'
  | 'notReviewed'
  | 'ciRed'
  | 'ciCanceled'
  | 'mergeRequest'
  | 'mainRed'
  | 'config'

// ReviewError mirrors api/v1alpha1.ReviewError: why one reviewer failed.
export interface ReviewError {
  reviewer: string
  kind: 'usageLimit' | 'transient' | 'other'
  // engine and resetAt: the limited engine and when it resets (usageLimit).
  engine?: string
  resetAt?: string
}

// MergeOverride mirrors api/v1alpha1.MergeOverride: an owner's "merge anyway".
export interface MergeOverride {
  issueIID: number
  sha: string
  by: string
  at: string
}

export interface DecisionRef {
  id: string
  title?: string
  url?: string
  superseded?: boolean
}

export interface AutopilotItem {
  iid: number
  state: 'Pending' | 'Active' | 'Merged' | 'Skipped'
  mergeRequestIID?: number
  mergeRequestURL?: string
  mergedSHA?: string
  markedReady?: boolean
  decisions?: DecisionRef[]
  revisitOf?: string
}

export interface AutopilotStatus {
  phase: AutopilotPhase
  trackingIssueIID?: number
  pausedReason?: string
  pauseKind?: PauseKind
  pausedFor?: number
  resumeWhenReady?: boolean
  items?: AutopilotItem[]
}

export interface PilotStatus {
  // proposals: open proposals on the tracking issue (Coordinate tasks).
  proposals?: ProposalView[]
}

export interface TaskStatus {
  phase?: TaskPhase
  podName?: string
  workspaceClaimName?: string
  message?: string
  agentTermination?: ContainerTermination
  agentMemoryLimit?: string
  run?: RunStatus
  ci?: CIStatus
  plan?: PlannedIssue[]
  autopilot?: AutopilotStatus
  pilot?: PilotStatus
  conditions?: Condition[]
}

export interface Task {
  namespace: string
  name: string
  project: string
  type: 'Spec' | 'Implement' | 'Coordinate'
  milestone?: MilestoneRef
  issue?: IssueRef
  suspend: boolean
  autopilot: boolean
  runRequest: number
  runPending: boolean
  stage: Stage
  createdAt: string
  status: TaskStatus
}

export interface Permissions {
  // transcript: the live transcript (and prompts) of the task's runs.
  transcript: boolean
  // gitlab: the project's GitLab is connected to the UI (thread after
  // connecting your account); steer: you may post comments.
  gitlab?: boolean
  steer?: boolean
  // chat: Desk chat with the task's agent (no GitLab token).
  chat?: boolean
}

export interface GitLabStatus {
  enabled: boolean
  url?: string
  connected?: boolean
  username?: string
  // login: where to connect an account; absent without an OAuth app.
  login?: string
  // fixed: connected with a configured token (no disconnect).
  fixed?: boolean
}

export interface NoteView {
  id: number
  author: string
  body: string
  system?: boolean
  createdAt?: string
}

export interface ThreadPart {
  target: 'issue' | 'mr' | 'tracking'
  iid: number
  webURL: string
  notes: NoteView[] | null
}

export type PilotAutonomy = 'propose' | 'managed'

// MilestoneView mirrors internal/ui/milestone.go. pausedReason and proposal
// summaries are raw agent and CI text: shown as text only.
export interface MilestoneView {
  id: number
  iid: number
  title: string
  webURL: string
  spec: Task
  autopilot?: {
    phase: AutopilotPhase
    pausedReason?: string
    waitingFor?: number
    // pauseKind and pausedFor: why and over which issue it paused; head is
    // that issue's pushed SHA (what "merge anyway" names).
    pauseKind?: PauseKind
    pausedFor?: number
    head?: string
    reviewErrors?: ReviewError[]
    mergeOverride?: MergeOverride
    items: QueueItem[]
  }
  tracking?: { iid: number; webURL: string }
  pilot?: {
    task: string
    autonomy: PilotAutonomy
    proposals: ProposalView[]
    costUSD: string
    phase?: TaskPhase
    message?: string
    podName?: string
  }
  tasks: Task[]
  costUSD: string
  permissions?: {
    gitlab: boolean
    steer: boolean
    chat: boolean
    transcript: boolean
    // merge: may send the owner-only merge override.
    merge?: boolean
  }
}

export interface QueueItem {
  iid: number
  title: string
  state: AutopilotItem['state']
  mergeRequestIID?: number
  mergeRequestURL?: string
  // task: the issue's implement task, if there is one.
  task?: string
}

export interface ProposalView {
  id: number
  summary: string
  postedAt?: string
}

export interface TaskResponse {
  project: ProjectSummary
  task: Task
  permissions?: Permissions
}

// TranscriptEntry is one line of a run's live transcript.
export interface TranscriptEntry {
  time?: string
  run?: string
  kind: 'step' | 'cli' | 'thinks' | 'says' | 'tool' | 'answers' | 'prompt'
  stepNo?: number
  step?: string
  status?: 'started' | 'done' | 'failed' | 'deferred'
  result?: string
  role?: string
  engine?: string
  model?: string
  readOnly?: boolean
  text?: string
  tool?: string
  input?: string
  system?: string
  prompt?: string
}

// Feature is a section of the UI the user may see (GET /api/v1/features).
// write and namespace: the user may start one-off researches, in namespace.
// history: the research database is configured (stream history available).
export interface Feature {
  name: 'factory' | 'research' | 'history' | 'chat'
  write?: boolean
  namespace?: string
}

export type ChatRole = 'user' | 'agent' | 'system'
export type ChatKind = 'message' | 'interrupt' | 'command'
export type ChatState = 'queued' | 'delivered' | 'answered' | 'failed' | 'deferred'

// ChatMessage is one row of GET .../tasks/{task}/chat (newest first).
export interface ChatMessage {
  id: number
  at: string
  author?: string
  authorName?: string
  role: ChatRole
  kind: ChatKind
  body?: string
  redacted?: boolean
  replyTo?: number
  state?: ChatState
  reason?: string
}

export interface ChatPage {
  messages: ChatMessage[]
}

export type StreamKind =
  | 'note'
  | 'run.started'
  | 'run.finished'
  | 'push'
  | 'ci'
  | 'review'
  | 'needs'
  | 'merged'
  | 'closed'
  | 'autopilot'
  | 'proposal'
  | 'chat.user'
  | 'chat.agent'

export interface StreamEvent {
  id: number
  at: string
  namespace: string
  project: string
  milestoneIID?: number
  issueIID?: number
  mergeRequestIID?: number
  task?: string
  kind: StreamKind
  actor?: string
  summary: string
  redacted?: boolean
  data: {
    body?: string
    bot?: boolean
    url?: string
    noteable?: 'issue' | 'mr'
    request?: number
    result?: string
    runSummary?: string
    costUSD?: string
    durationSec?: number
    runCount?: number
    sha?: string
    branch?: string
    state?: string
    pipelineID?: number
    pipelineURL?: string
    failedJobs?: string[]
    round?: number
    blocking?: number
    reviewErrors?: number
    reason?: string
    message?: string
    mergeRequestURL?: string
    phase?: string
    proposalID?: number
    proposalState?: string
    // Desk chat (chat.user / chat.agent).
    role?: ChatRole
    kind?: ChatKind
    authorName?: string
    replyTo?: number
  }
}

export interface StreamPage {
  events: StreamEvent[]
  older?: string
  newest?: string
  history: boolean
}

export interface HistoryDay {
  day: string
  runs: number
  merged: number
  costUSD: number
  events: number
}

export interface HistoryTotals {
  runs: number
  merged: number
  costUSD: number
  events: number
  medianCycleMin?: number
}

export interface HistoryResponse {
  days: HistoryDay[]
  totals: HistoryTotals
  previous: HistoryTotals
}

export interface InboxItem {
  namespace: string
  project: string
  kind: 'task' | 'proposal' | 'pause'
  // pauseKind: why the milestone's autopilot paused (kind pause).
  pauseKind?: PauseKind
  task?: string
  issueIID?: number
  milestoneIID?: number
  proposalID?: number
  title: string
  reason: string
  at?: string
  url?: string
}

export interface InboxResponse {
  items: InboxItem[]
}

export type ResearchPhase = 'Scheduled' | 'Running' | 'Waiting' | 'Succeeded' | 'Failed'

export interface ResearchRunStatus {
  id?: string
  startedAt?: string
  finishedAt?: string
  engine?: string
  model?: string
  effort?: string
  reason?: string
  outcome?: 'succeeded' | 'degraded' | 'failed' | 'limited'
  message?: string
  costUSD?: string
}

// ResearchSummary is an AgentResearch as the research pages show it.
export interface ResearchSummary {
  namespace: string
  name: string
  view: 'newsflash' | 'report'
  depth: 'quick' | 'deep'
  schedule?: string
  timeZone?: string
  phase?: ResearchPhase
  lastRun?: string
  nextRun?: string
  lastEngine?: string
  lastModel?: string
  run?: ResearchRunStatus
  skippedReason?: string
  waiting?: { engine: string; until: string }
  oneOff: boolean
  createdBy?: string
  // canDelete: the user may delete it here (their own one-off).
  canDelete: boolean
}

// ResearchDetail is a research with what its page shows besides the summary.
// agent is absent for auto.
export interface ResearchDetail extends ResearchSummary {
  prompt: string
  topics?: string[]
  sources?: { url: string; name?: string; topic?: string }[]
  agent?: { engine: string; model: string; effort?: string }
}

// ResearchRun is a stored run (internal/ui/research.go).
export interface ResearchRun {
  id: string
  startedAt: string
  finishedAt?: string
  status: 'running' | 'succeeded' | 'degraded' | 'failed' | 'limited'
  engine?: string
  model?: string
  effort?: string
  costUSD: string
  choiceReason?: string
  error?: string
  warnings?: string[]
  expiresAt: string
}

// Blocks of schema v1 (internal/research/blocks), validated by the server.
// Their text is untrusted agent output: rendered as text only.
export interface HeadingBlock {
  type: 'heading'
  text: string
  level?: number
}

export interface MarkdownBlock {
  type: 'markdown'
  text: string
}

// ResearchItem is a piece of news or a finding; new: first seen in this run.
export interface ResearchItem {
  topic?: string
  title: string
  summary?: string
  url: string
  source?: string
  publishedAt?: string
  importance: number
  new?: boolean
}

export interface ItemsBlock {
  type: 'items'
  items: ResearchItem[]
}

export interface TableBlock {
  type: 'table'
  columns: string[]
  rows: string[][]
}

export interface StatsBlock {
  type: 'stats'
  stats: { label: string; value: string; delta?: string }[]
}

export interface LinksBlock {
  type: 'links'
  links: { title: string; url: string }[]
}

export type Block = HeadingBlock | MarkdownBlock | ItemsBlock | TableBlock | StatsBlock | LinksBlock

export interface ResearchResult {
  runID: string
  title: string
  summary: string
  blocks: Block[]
  createdAt: string
  expiresAt: string
  warnings?: string[]
}

// ResearchResponse is a research page (GET /researches/{ns}/{name}, run is
// the latest result's) or a run page (…/runs/{id}). run and result are null
// before there is one.
export interface ResearchResponse {
  research: ResearchDetail
  run: ResearchRun | null
  result: ResearchResult | null
}

// ResearchRunsPage is GET /researches/{ns}/{name}/runs (?before=, ?limit=).
export interface ResearchRunsPage {
  runs: ResearchRun[]
}

// ResearchSearchHit is one hit of GET /api/v1/research/search?q=.
export interface ResearchSearchHit {
  runID: string
  namespace: string
  research: string
  title: string
  summary: string
  createdAt: string
}

export interface ResearchSearchPage {
  hits: ResearchSearchHit[]
}

// ResearchForm is GET /api/v1/research/form: what the one-off form offers.
// A project without a writer login is not available, reason says why.
export interface ResearchForm {
  namespace: string
  projects: {
    name: string
    available: boolean
    reason?: string
    models: { engine: string; model: string; label: string }[]
  }[]
  depths: ResearchSummary['depth'][]
  views: ResearchSummary['view'][]
  efforts: string[]
}

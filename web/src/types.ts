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

export interface AutopilotOverview {
  milestoneIID: number
  title: string
  phase: AutopilotPhase
  pausedReason?: string
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
  pausedFor?: number
  resumeWhenReady?: boolean
  items?: AutopilotItem[]
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
  conditions?: Condition[]
}

export interface Task {
  namespace: string
  name: string
  project: string
  type: 'Spec' | 'Implement'
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

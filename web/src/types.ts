// The view types of the UI API come from Go: generated/views.ts (internal/ui),
// generated/api.ts (the CRD types of api/v1alpha1) and generated/store.ts
// (the store types views carry as they are, internal/research/store), by
// make ts-types (tygo.yaml). Do not mirror a Go view here: change the Go
// struct and regenerate. What stays here is web-only: the string unions and
// open shapes the Go views leave untyped (tstype tags point here). Responses
// are named Go structs and are not declared here.

export type { SpendTotals } from './generated/store'

export type {
  Me,
  ProjectSummary,
  ProjectDetail,
  Milestone,
  Task,
  Stage,
  AutopilotOverview,
  Cost,
  MilestoneView,
  QueueItem,
  ProposalView,
  PromptView,
  PromptOptionView,
  PromptAnswerView,
  NoteView,
  ThreadPart,
  Feature,
  StreamEvent,
  StreamPage,
  HistoryDay,
  HistoryTotals,
  HistoryResponse,
  InboxItem,
  ResearchSummary,
  ResearchDetail,
  ResearchRun,
  ResearchResult,
  ResearchForm,
  ChatMessageView as ChatMessage,
  ChatPage,
  GitLabStatusView as GitLabStatus,
  Entry as TranscriptEntry,
  SearchHit as ResearchSearchHit,
  SpendView as Spend,
  SpendChildView as SpendChild,
  SpendBreakdownView as SpendBreakdown,
  SubagentBreakdownView as SubagentBreakdown,
  StepLinesView as StepLines,
  ModelSplitView as ModelSplit,
  ExitView,
  ExitStatus,
  PauseHistoryView,
  PauseView,
  CompanionView,
  CompanionChip,
  OverviewView,
  WorkingNow,
  WorkingNext,
  MilestoneRow,
  MilestoneList,
  MilestoneState,
  MilestoneIssueRow,
  ReleaseView,
  IssueView,
  MilestoneProgress,
  RetrospectiveView,
  FollowUpView,
  FollowUpDraftView,
  FollowUpMilestoneView,
  CostDriverView,
  TaskResponse,
  TaskPermissions as Permissions,
} from './generated/views'

export type {
  Condition,
  UsageLimit,
  UsageWindow,
  EngineUsage,
  MilestoneRef,
  IssueRef,
  TaskPhase,
  CIState,
  CIStatus,
  RunStatus,
  ContainerTermination,
  PlannedIssue,
  AutopilotPhase,
  AutopilotStatus,
  AutopilotItem,
  BudgetStatus,
  PauseKind,
  ReviewError,
  MergeOverride,
  PauseExit,
  DecisionRef,
  PilotAutonomy,
  PilotStatus,
  PromptKind,
  PromptState,
  ResearchPhase,
  ResearchRunStatus,
  AgentTaskStatus as TaskStatus,
} from './generated/api'

export type ChatRole = 'user' | 'agent' | 'system'
export type ChatKind = 'message' | 'interrupt' | 'command'
export type ChatState = 'queued' | 'delivered' | 'answered' | 'failed' | 'deferred'

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
  | 'autopilot.paused'
  | 'autopilot.resumed'
  | 'pilot.writeFailed'
  | 'retrospective'
  | 'prompt.posted'
  | 'prompt.answered'
  | 'prompt.closed'
  | 'chat.user'
  | 'chat.agent'
  | 'checkpoint'
  | 'release.decided'
  | 'release.started'
  | 'release.ended'

// StreamEventData is a stream event's data (Go: map[string]any): the keys
// of all kinds.
export interface StreamEventData {
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
  // checkpoint: a checkpoint no call carries (also sha and reason).
  run?: number
  step?: number
  files?: number
  added?: number
  deleted?: number
  // prompt.* events.
  promptID?: string
  promptKind?: string
  optionLabel?: string
  by?: string
  // Desk chat (chat.user / chat.agent).
  role?: ChatRole
  kind?: ChatKind
  authorName?: string
  replyTo?: number
  // retrospective: the GitLab note and the number of follow-ups.
  noteID?: number
  followUps?: number
  // release.decided / release.started / release.ended (also by, state,
  // reason, pipelineID and pipelineURL); why and reason are untrusted.
  releaseID?: number
  afterIssue?: number
  final?: boolean
  why?: string
  releasedSHA?: string
  issues?: number[]
  mergeRequests?: number[]
  attempt?: number
  version?: string
  releaseURL?: string
}

export type SpendLevel = 'milestone' | 'issue' | 'run' | 'call'

// Blocks of schema v1 (internal/research/blocks), validated by the server;
// the result view passes them on as []any. Their text is untrusted agent
// output: rendered as text only.
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

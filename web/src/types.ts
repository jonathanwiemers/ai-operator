// The view types of the UI API come from Go: generated/views.ts (internal/ui),
// generated/api.ts (the CRD types of api/v1alpha1) and generated/store.ts
// (the store types views carry as they are, internal/research/store), by
// make ts-types (tygo.yaml). Do not mirror a Go view here: change the Go
// struct and regenerate. What stays here is web-only: the string unions and
// open shapes the Go views leave untyped (tstype tags point here). Responses
// are named Go structs and are not declared here.

export type { Pause, PauseHistory, SpendTotals } from './generated/store'

export type {
  About,
  Me,
  ProjectSummary,
  ProjectDetail,
  Task,
  Stage,
  Cost,
  MilestoneView,
  QueueItem,
  ProposalView,
  PromptView,
  PromptOptionView,
  NoteView,
  ThreadPart,
  Feature,
  StreamEvent,
  StreamPage,
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
  SpendView as Spend,
  SpendChildView as SpendChild,
  SpendBreakdownView as SpendBreakdown,
  SubagentBreakdownView as SubagentBreakdown,
  StepLinesView as StepLines,
  ExitView,
  ExitStatus,
  HoldView,
  CompanionView,
  OverviewView,
  WorkingNow,
  MilestoneRow,
  MilestoneList,
  MilestoneState,
  MilestoneIssueRow,
  ReleaseView,
  IssueView,
  MilestoneProgress,
  FollowUpView,
  FollowUpDraftView,
  FollowUpMilestoneView,
  CostDriverView,
  TaskResponse,
  ProblemStatus,
  ProblemView,
  ProblemList,
  ProblemThread,
} from './generated/views'

export type {
  UsageLimit,
  UsageWindow,
  EngineUsage,
  TaskPhase,
  CIState,
  RunStatus,
  AutopilotItem,
  PauseKind,
  ReviewError,
  DecisionRef,
  ResearchPhase,
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
  | 'item.skipped'
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
  | 'milestone.started'
  | 'problem.opened'
  | 'problem.resolved'
  | 'hold.set'
  | 'hold.cleared'
  | 'pilot.acted'

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
  // autopilot, milestone.started and release.*: the milestone's title.
  title?: string
  afterIssue?: number
  final?: boolean
  why?: string
  releasedSHA?: string
  // A started release's issues; milestone.started: the number planned.
  issues?: number[] | number
  mergeRequests?: number[]
  attempt?: number
  version?: string
  // problem.opened / problem.resolved: why it got stuck and who resolved
  // it; hold.set / hold.cleared carry holdKind, reason and since (the
  // hold's start, RFC 3339); pilot.acted why, lines, ran and by. All
  // untrusted text.
  holdKind?: string
  cause?: string
  resolvedBy?: string
  lines?: string[]
  ran?: boolean
  releaseURL?: string
}

export type SpendLevel = 'milestone' | 'issue' | 'run' | 'call'

// Blocks of schema v1 (internal/research/blocks), validated by the server;
// the result view passes them on as []any. Their text is untrusted agent
// output: rendered as text only.
interface HeadingBlock {
  type: 'heading'
  text: string
  level?: number
}

interface MarkdownBlock {
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

interface ItemsBlock {
  type: 'items'
  items: ResearchItem[]
}

interface TableBlock {
  type: 'table'
  columns: string[]
  rows: string[][]
}

interface StatsBlock {
  type: 'stats'
  stats: { label: string; value: string; delta?: string }[]
}

interface LinksBlock {
  type: 'links'
  links: { title: string; url: string }[]
}

export type Block = HeadingBlock | MarkdownBlock | ItemsBlock | TableBlock | StatsBlock | LinksBlock

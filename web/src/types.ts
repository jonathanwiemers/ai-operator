// The view types of the UI API come from Go: generated/views.ts (internal/ui)
// and generated/api.ts (the CRD types of api/v1alpha1), by make ts-types
// (tygo.yaml). Do not mirror a Go view here: change the Go struct and
// regenerate. What stays here is web-only: the string unions and open shapes
// the Go views leave untyped (tstype tags point here), and the response
// envelopes the handlers build as maps.

import type * as views from './generated/views'

export type {
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
  GitLabStatusView as GitLabStatus,
  Entry as TranscriptEntry,
  SearchHit as ResearchSearchHit,
  SpendView as Spend,
  SpendTotalsView as SpendTotals,
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

// Me is GET /api/v1/me.
export interface Me {
  username: string
  roles: string[] | null
  authMode: 'oidc' | 'none'
}

// Permissions is what the task page may offer (GET .../tasks/{task}).
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

// TaskResponse is GET .../tasks/{task}.
export interface TaskResponse {
  project: views.ProjectSummary
  task: views.Task
  // prompts: the agent's questions (Implement tasks), newest first.
  prompts: views.PromptView[]
  cost: views.Cost
  permissions?: Permissions
}

export type ChatRole = 'user' | 'agent' | 'system'
export type ChatKind = 'message' | 'interrupt' | 'command'
export type ChatState = 'queued' | 'delivered' | 'answered' | 'failed' | 'deferred'

// ChatPage is GET .../tasks/{task}/chat (newest first).
export interface ChatPage {
  messages: views.ChatMessageView[]
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
  | 'prompt.posted'
  | 'prompt.answered'
  | 'prompt.closed'
  | 'chat.user'
  | 'chat.agent'

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
}

export type SpendLevel = 'milestone' | 'issue' | 'run' | 'call'

// InboxResponse is GET /api/v1/inbox.
export interface InboxResponse {
  items: views.InboxItem[]
}

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

// ResearchResponse is a research page (GET /researches/{ns}/{name}, run is
// the latest result's) or a run page (…/runs/{id}). run and result are null
// before there is one.
export interface ResearchResponse {
  research: views.ResearchDetail
  run: views.ResearchRun | null
  result: views.ResearchResult | null
}

// ResearchRunsPage is GET /researches/{ns}/{name}/runs (?before=, ?limit=).
export interface ResearchRunsPage {
  runs: views.ResearchRun[]
}

// ResearchSearchPage is GET /api/v1/research/search?q=.
export interface ResearchSearchPage {
  hits: views.SearchHit[]
}

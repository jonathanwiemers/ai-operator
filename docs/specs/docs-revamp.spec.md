# Docs: a clear path from install to autopilot, in plain language

Status: the milestone's spec, run by autopilot. The milestone description in
GitLab holds the full spec with its decisions and requirements; this file
keeps the layout, the rules every merge request follows, the
[Inventory](#inventory) of the old pages and the [Steering log](#steering-log).

## Goal

The milestone's six goals, in short:

1. A new user finds the docs in four sections, Getting started, Guides,
   Reference and Concepts, and reaches any page in two clicks from
   [`docs/README.md`](../README.md).
2. Getting started alone takes a new user from installing the operator to a
   first planned milestone.
3. User pages are in plain language, under 400 lines, and name no source
   files. Internals live in a contributor section.
4. CI keeps it that way: broken links and anchors, pages over the cap and
   file paths in user pages fail `make test`.
5. No fact is lost and no outdated one survives. The inventory below proves
   it.
6. The README tells a new visitor what the project is today: a milestone
   runs on autopilot, the pilot handles what goes wrong, and you steer from
   the Desk.

## Layout

```
docs/
  README.md            the map: every page, one line each
  getting-started/     install, first milestone
  guides/              one task per page
  reference/           commands, AgentProject fields, labels, Desk API
  concepts/            how it works, roles, security, glossary
  contributing/        design, web UI development, releasing, sandbox test plan
  specs/               archive with an index
  assets/
```

Every folder has a `README.md` that lists its pages in reading order. A
merge request that adds a page adds it to its folder README and to the map
in `docs/README.md`. Folder READMEs link only pages that exist.

## Rules for every merge request

- **Plain language.** In the four user folders (`getting-started/`,
  `guides/`, `reference/`, `concepts/`), write for someone who runs the
  operator and writes milestones, not for someone who reads the code. "You",
  active voice, short sentences. One page is one task or one topic. A page
  opens with a short paragraph on what it is for and ends with "Next" links.
  A term is explained on first use and linked to the glossary. Examples come
  before tables, and every configuration page has a complete example that
  works.
- **No source refs in user pages.** No file paths, Go or TypeScript
  identifiers, function names or line refs in the four user folders. CRD
  field names, commands, labels, Helm values and URLs are fine. Internals go
  to `contributing/`.
- **400-line cap.** Pages in the four user folders and every folder README
  stay under 400 lines. A topic that needs more becomes several pages.
  `contributing/` and `specs/` have no cap.
- **Desk first.** Guides show how to do a thing in the Desk first and the
  `/agent` comment second. GitLab stays where the spec is written and where
  the merge requests live.
- **Today's behaviour wins.** Moving a section is not copying it. Check
  each statement against the code, the CRD defaults and the chart values,
  and fix what is stale. Default values are stated once, in `reference/`,
  and linked elsewhere.
- **Move, don't copy.** The merge request that adds the new text removes the
  old text, so no fact exists twice and none disappears. It sets the status
  of its rows below.
- **The docs check.** `make test` runs `internal/docscheck` (R2): relative
  links and their anchors, every `docs/….md` ref in code and config, the cap
  and the source-ref rule, and the outside entry points `docs/README.md`
  and `README.md#quick-start`. Run `go test ./internal/docscheck/` and fix
  what it reports in the same merge request.

## Inventory

One row per `##` and `###` heading of the eight old pages, in page order,
taken from `main` at 90824ec (2026-11-16). Lines inside fenced code blocks
are not headings. The columns:

- **Old page**: the page in `docs/` the heading is on today.
- **Heading**: the heading with its level.
- **Target**: the new page that takes the section, or "dropped". A merge
  request may change a target when a page has to split; it says so in the
  [Steering log](#steering-log).
- **MR**: the requirement label of the merge request that moves the row.
- **Status**, one of:
  - `open`: not moved yet.
  - `done`: moved, every statement still true today.
  - `done; updated: <what>`: moved, and the statements named were fixed to
    today's behaviour.
  - `dropped: <why>`: not moved, with a reason a reviewer can check.

Rows: `usage.md` 38, `ui.md` 57, `configuration.md` 34, `commands.md` 10,
`research.md` 15, `design.md` 28, `releasing.md` 6, `sandbox-test-plan.md`
28; 216 in all.

| Old page | Heading | Target | MR | Status |
|----------|---------|--------|----|--------|
| usage.md | ## One-time setup in GitLab | getting-started/install.md | R3 | done; updated: the label table, state diagram and needs-input/blocked text link reference/labels.md instead of repeating them; labels created with a GitLab API loop, the scripts linked by URL; the board created in GitLab; the bot may merge into the default branch (autopilot); the GitLab-pipeline note on the repository itself dropped (contributing/releasing.md has it) |
| usage.md | ### Webhooks (optional) | getting-started/install.md | R3 | done; updated: an HTTPRoute example for the receiver; the sync behaviour while deliveries arrive kept once in reference/agentproject.md and the chart README |
| usage.md | ## 1. Write the milestone spec | guides/write-a-milestone.md | R4 | done; updated: Desk first (*Plan milestone*); the operator, not the planner, creates the issues; dependencies are a *Depends on* list; a release without a reason counts as "not after this issue"; besides the pilot, an owner's `parallel N` and an accepted budget raise also write the description; poll interval and `infraRetryAfter` linked to reference/; `status.run.summary` dropped: Kubernetes-side failure details stay with "Watching from Kubernetes" (R7b) |
| usage.md | ### Dependencies as issue links | guides/write-a-milestone.md | R4 | done |
| usage.md | ## 2. Answer questions and hand the issue over | guides/answer-and-hand-over.md | R4 | done; updated: Desk first (Inbox, *Questions from the agent*); the needs-input prompt text from "3. The agent works" moved here; owner-subjects alone may answer in the Desk, separate from `/agent owners` (the old text mixed them up); "a `/agent` comment still works as an answer" corrected: it starts a new run that closes the open prompts; planner decisions on an open issue are revisited by comment; the label hand-over is one section; the chat command JSON and `spec.source.promptAnswers` are already in contributing/design.md "Answering prompts" |
| usage.md | ## 3. The agent works | guides/while-the-agent-works.md | R4 | done; updated: the Desk task page first; a failed run's progress comment keeps a short line, not the reason; CI fix runs on a draft do have steps and the step-size gate; review rounds and retries linked to reference/ instead of stated; `status.run.*` fields, the hook timing and the 3 s edit interval are already in contributing/design.md |
| usage.md | ### Commit messages | guides/while-the-agent-works.md | R4 | done; updated: an example message; schema and runner internals are already in contributing/design.md "Checkpoints and Conventional Commits" |
| usage.md | ### Toolchains the agent installs | guides/while-the-agent-works.md | R4 | done; updated: an example `.mise.toml`; env variables and directories are already in contributing/design.md; the failed-install report is the agent's, not the runner's |
| usage.md | ### Owners and pings | guides/owners-and-pings.md | R4 | done; updated: target changed, see the steering log; `owners` and `owner-subjects` explained side by side; failed-run pings are not sent on autopilot; question pings are in the first question's comment; reply limit linked to reference/commands.md |
| usage.md | ## CI feedback | guides/while-the-agent-works.md | R4 | done; updated: fix rounds start again with every run that is no CI fix, and runs the pilot starts keep the count; merged-results pipelines are followed (reference/lifecycle-and-ci.md said they are not, fixed there too); numbers linked to `spec.ci` |
| usage.md | ## Main health | guides/while-the-agent-works.md | R4 | done; updated: a canceled pipeline is left alone once a newer pipeline replaced it; only the first 3 jobs' logs are attached; an owner who accepts any flaky-fix proposal needs one occurrence; `retriedJobIDs` was already in contributing/design.md "Main health"; the status jsonpath, log limits, test-name patterns and title limits moved there |
| usage.md | ## Merge conflicts | guides/while-the-agent-works.md | R4 | done; updated: GitLab API fields and merge commit details are already in contributing/design.md "Merge conflicts"; the conflict role linked to reference/ |
| usage.md | ## 4. Iterate | guides/iterate-and-finish.md | R4 | done; updated: Desk first: the task page's *Conversation* box (GitLab comments) and *Chat with the agent* (chat turns between runs, no run started) |
| usage.md | ### Decisions | guides/iterate-and-finish.md | R4 | done; updated: long decision lists continue in a second comment; follow-ups also copy model and effort labels; the Desk's Revisit button works after the merge only (an open issue gets "still open: comment there"), first-milestone.md corrected too; the chat command JSON dropped: it is the Revisit button's request, in contributing/design.md "Pilot" |
| usage.md | ## 5. Finish | guides/iterate-and-finish.md | R4 | done; updated: autopilot merges and closes; task, pod and workspace go on the next poll; closing without a merge leaves the merge request open |
| usage.md | ## Autopilot: a milestone end to end | guides/autopilot.md | R5 | done; updated: Desk first (*Plan milestone* adds the markers, the milestone page, *Run next* and *Clear mark*, the parallel stepper); a checklist with an example to turn it on; waves are a Desk view of the plan only; the landing lane and holds told here as "Several issues at once"; the end names the retrospective and the closing of the milestone |
| usage.md | ### Several milestones at once | guides/autopilot.md | R5 | done; updated: the Desk buttons *Run next* and *Clear mark*; `maxConcurrentRuns` linked to reference/; `startRequestedAt`, `spec.runRequest` and `queuedBehind` are already in contributing/design.md |
| usage.md | ### The merge gate | guides/autopilot.md | R5 | done; updated: condensed; the merge `sha`, the mechanical-rebase rules, `autoReReviews` and `mergeOverride` are already in contributing/design.md "Autopilot"; companion details stay with "Companion repository" (R7a) |
| usage.md | ### When it pauses | guides/pauses.md | R5 | done; updated: Desk first (pause and hold cards and their buttons); each kind says what the pilot tries; `stalled` names the watchdog and the Desk restart, `/agent restart` is no GitLab command; defaults linked to reference/; the ways-out table, the pause records (`resolvedBy`, `via`, `pauseRunBy`) and the needs-input reason rules moved to contributing/design.md "Autopilot"; rejected status writes kept short, their internals already in contributing/design.md "Pilot" |
| usage.md | ### Budget | guides/budgets-and-spend.md | R7a | done; updated: Desk first (the budget prompt on the pause card, the Inbox item, the Costs page's *Budgets*); the pilot's calls count towards the milestone budget; calls without a known cost add nothing to a budget (the old text said they count $0); cursor `auto` is not tied to the `fast` profile and the reviewers, any role set to it is unmetered; thresholds passed in one poll get one heads-up, the highest; the spend report SQL moved to contributing/design.md "Spend report script" and the guide describes what it answers (decision D515-P2); "What the spend counts" internals moved to contributing/design.md, the rest of the budget internals to its "Budgets in detail" |
| usage.md | ### Companion repository | guides/cross-repo-changes.md | R7a | done; updated: Desk first (the *Companions* list, the Inbox item for a companion ready to merge, the *companion* marker on *Spend*); an example with the three gates; the 1 MiB cap counts added and changed files, not only new ones; an invalid `/agent repo` line is named on the tracking issue only when there is one; a companion of a skipped issue does not hold the milestone's done; any non-claude implementer moves to claude, not only cursor (one sentence, linked to the fallback engine); status fields, prompt sections, CI and landing internals moved to contributing/design.md "Companions in detail" (the rest was already in "Cross-repo" and "Security") |
| usage.md | ### Releases | guides/releases.md | R7a | done; updated: Desk first (release rows and versions on the *Issues* tab, release events on *Pilot*); running decisions are checked first, then the rest in the order they were made (not "oldest first"); the next version is one more than this month's highest `N` among the newest 100 tags; `/agent endMilestone` drops only a final release that failed, not one that waits or runs; `/agent no-release #N` drops the releases after exactly #N; the stale "other deciders come in later changes" note dropped; record fields, history events, plan sync, the pilot's watermark and the release-note escaping moved to contributing/design.md "Releases" (decision D515-P1: the CRD comment points at the guide now) |
| usage.md | ### Commands on the tracking issue | guides/autopilot.md | R5 | done; updated: a short table, every command and its limits linked to reference/commands.md; `/agent release` and `/agent no-release` added (the old table lacked them); status fields dropped (in contributing/design.md) |
| usage.md | ### Tracking issue | guides/autopilot.md | R5 | done; updated: the description lists the holds |
| usage.md | ### Switching it off | guides/autopilot.md | R5 | done; updated: changing the plan is its own section |
| usage.md | ## Prompts | guides/steer-from-the-desk.md | R6 | done; updated: Desk first (where prompts appear, one click or your own words, the GitLab comment second); the kinds, Q-ids, the newest 20 in status, the history events and legacy P<n> moved to contributing/design.md "Prompts in GitLab" |
| usage.md | ## Talking to the pilot | guides/pilot.md | R5 | done; updated: Desk first (*Pilot* tab, *Ask the pilot*, answers in your own words); `pilot.autonomy` is `managed` when unset on autopilot; defaults linked to reference/; the actions in plain words, the command forms linked to reference/commands.md; what its state lists, the classification values, five waiting answers and the legacy "P<n>" proposals moved to contributing/design.md "Pilot" |
| usage.md | ### Spec changes through the pilot | guides/pilot.md | R5 | done |
| usage.md | ### The retrospective | guides/pilot.md | R5 | done; updated: the Desk steps first; the facts, their size and trimming order moved to contributing/design.md "Pilot"; the draft command and the Desk endpoints are already there and in "Desk API" |
| usage.md | ## When a run is cut off | guides/when-runs-go-wrong.md | R5 | done; updated: target changed, see the steering log; the trailer, the push without CI and the fold are already in contributing/design.md, the secret-name rule added there |
| usage.md | ## Usage and rate limits | guides/when-runs-go-wrong.md | R5 | done; updated: target changed; split into "Usage limits" and "When GitLab is slow"; the checkpoint text is not repeated; the poll deadline, `Synced` and the log lines are in contributing/design.md "Token economy"; engine usage internals already in "Engines, profiles and subscriptions" |
| usage.md | ## Issues that stay open | guides/when-runs-go-wrong.md | R5 | done; updated: target changed; the workspace goes after `retentionPeriod` without activity, not after the last run; the times linked to reference/lifecycle-and-ci.md |
| usage.md | ### Agent notes | guides/when-runs-go-wrong.md | R5 | done; updated: target changed |
| usage.md | ## Watching progress | guides/desk.md | R6 | done; updated: the Desk steps went into the views; the GitLab columns table reduced to a sentence with `workflow.assignBot` linked (decision D514-P2) |
| usage.md | ## Watching from Kubernetes | guides/watch-from-kubernetes.md | R7b | done; updated: `Waiting` also covers out-of-memory retries and budget waits; AgentProject and AgentTask columns (the CI column and the resume time were missing, `-o wide` adds the resume time and the merge request), the task phases explained (Idle, Hibernated and Archived follow the pod and workspace lifecycle), `interruptions` listed with `status.run`, `spec.suspend` with `kubectl patch` examples |
| usage.md | ### Run logs | guides/watch-from-kubernetes.md | R7b | done; updated: *open merge request* runs before the agent (only without a merge request), not after the push; *request review* added; steps of issues worked in steps and of checkpoints mentioned; `logLevel: error` drops the step lines too; the Desk's Live tab named |
| usage.md | ### When the agent container dies | guides/watch-from-kubernetes.md | R7b | done; updated: the unschedulable pod shows in `status.message` (`kubectl describe`), not in `-o wide`; the `maxMemory` default is linked in reference/ instead of repeated |
| usage.md | ### When the whole pod is lost | guides/watch-from-kubernetes.md | R7b | done; updated: the runner, not the operator, gives up after 3 restarts; the infra retry time links reference/ for its default |
| ui.md | ## Screenshots | guides/desk.md | R6 | done; updated: each screenshot sits with its view in guides/desk.md, the Costs page in guides/steer-from-the-desk.md |
| ui.md | ## Desk and history | guides/desk.md | R6 | done; updated: one section per view; the URLs, `?tab=` rules, event kinds per tab, stored milestones, cost chip tooltip, context panel and pause history API moved to contributing/web-ui.md "Desk behaviour in detail"; engine usage is in the rail, the *Working now* cards and the Costs page, not on the Board tab; the stream chips start with *All*; Revisit is on the queue's decisions (Issues tab and context panel) for owners with the `chat` grant |
| ui.md | ### Navigation | guides/desk.md | R6 | done; updated: rail, breadcrumbs, search and live updates in "Find your way", the bottom bar in steer-from-the-desk.md "On a phone"; search finds projects, milestones, issues and merge requests and pages; crumb, bar and about-line details moved to contributing/web-ui.md "Navigation" |
| ui.md | ### Queue and Finishing | guides/steer-from-the-desk.md | R6 | done; target changed to guides/steer-from-the-desk.md (it is an action); the API fields and command bodies moved to contributing/web-ui.md "Queue and Finishing" |
| ui.md | ### Planner | guides/steer-from-the-desk.md | R6 | done; target changed to guides/steer-from-the-desk.md "Plan a milestone", the steps stay in guides/write-a-milestone.md; defaults checked against the chart values; endpoints and storage moved to contributing/web-ui.md "Planner" |
| ui.md | ### Spend | guides/budgets-and-spend.md | R7a | done; updated: "Step commits" is a note under the implementer-steps table and "Gate blocks per call" a column of the subagents table, not tables of their own; the field-by-field text, the query parameters and the API moved to contributing/web-ui.md "Spend tab in detail" |
| ui.md | ### Costs page | guides/budgets-and-spend.md | R7a | done; updated: an example address with filters; the CSV columns, quoting, URL parameter values, buckets, chunk budget and e2e cases moved to contributing/web-ui.md "Costs page"; `ui.md` deleted |
| ui.md | ### Agent calls on the Task page | guides/desk.md | R6 | done; updated: a bullet of the *Calls* tab; the line counting, gate counts, checkpoint reasons and chart details moved to contributing/web-ui.md "Agent calls" |
| ui.md | ### Notifications | guides/desk.md | R6 | done; updated: the dedup rules moved to contributing/web-ui.md "Notifications"; the History part to its "History", the `conversation` grant to "Who sees what" |
| ui.md | ## Platform | guides/desk.md | R6 | done; updated: a short "Research" section; the block renderer and markdown subset moved to contributing/web-ui.md "Research results and markdown" |
| ui.md | ## What it shows | guides/desk.md | R6 | done; updated: the task page as a list of its tabs; *Live* also needs a pod log source, *Logs* owners with `steer`, *Restart run* owners with `chat`; chat, prompts and pause cards went to guides/steer-from-the-desk.md; the command routing, prompt command table and fallback pause actions moved to contributing/web-ui.md "Chat commands" and "Pause card actions"; the pilot's *Accept*/*Reject* are per open proposal |
| ui.md | ## Enable it | getting-started/turn-on-the-desk.md | R3 | done |
| ui.md | ### 1. Create the OIDC client | getting-started/turn-on-the-desk.md | R3 | done; updated: the Refresh Token grant type named in the Zitadel steps; the username left at its default (email) |
| ui.md | ### 2. Create the Secret | getting-started/turn-on-the-desk.md | R3 | done |
| ui.md | ### 3. Set the chart values | getting-started/turn-on-the-desk.md | R3 | done; updated: the rule grants `chat` and the chat writer login is set, which answering prompts needs, and `spec.agents.chat` is turned on in the project |
| ui.md | ### 4. Staying logged in | getting-started/turn-on-the-desk.md | R3 | done; updated: defaults left to the chart README; renewal internals are in contributing/design.md "Desk session model" |
| ui.md | ## Access rules | guides/desk.md | R6 | done; updated: "Who sees what", with what each grant unlocks (`steer` also plans, logs and follow-ups for owners) and the `owner-subjects` line |
| ui.md | ### Research rules | guides/desk.md | R6 | done; updated: the one-off research rules (namespace, names, creator, limits, audit) moved to contributing/web-ui.md "One-off researches" |
| ui.md | ### Claim mapping | guides/desk.md | R6 | done; updated: links the provider expressions in getting-started/turn-on-the-desk.md |
| ui.md | ## Live transcript | guides/desk.md | R6 | done; target changed to guides/desk.md (a view, not an action); how the logs are read moved to contributing/web-ui.md "Live transcript" |
| ui.md | ## Steering | guides/steer-from-the-desk.md | R6 | done; updated: "GitLab comments from the Desk", Desk chat first in "Chat with an agent" |
| ui.md | ### Set it up | guides/steer-from-the-desk.md | R6 | done |
| ui.md | ### One user, a fixed token | guides/steer-from-the-desk.md | R6 | done; updated: the Planner or Reporter role for creating milestones moved here from "How it works" |
| ui.md | ### How it works | guides/steer-from-the-desk.md | R6 | done; updated: limits in plain words; thread polling, the pilot prefix, follow-up text rules, token cookie and target checks moved to contributing/web-ui.md "GitLab steering"; the follow-up and plan creation are in guides/pilot.md and "Plan a milestone" |
| ui.md | ## Without login | getting-started/turn-on-the-desk.md | R3 | done; updated: the user name is `anonymous`; the DNS rebinding detail moved to contributing/web-ui.md "Security" (R6) |
| ui.md | ## Security | guides/desk.md | R6 | done; updated: what the login protects in guides/desk.md; the mechanics (cookies, redirects, signing in again, CSP, the localhost-only check against DNS rebinding) moved to contributing/web-ui.md "Security" (decision D514-P1) |
| ui.md | ## API | reference/desk-api.md | R8b | done; updated: endpoints grouped, each with its purpose and the grant it needs, checked against the router (48 routes); the answer shapes, parameters and error codes moved to contributing/design.md "Desk API" |
| ui.md | ## Development | contributing/web-ui.md | R10b | done |
| ui.md | ## Checks | contributing/web-ui.md | R10b | done |
| ui.md | ### Docs screenshots | contributing/web-ui.md | R10b | done |
| ui.md | ## Bundle budget | contributing/web-ui.md | R10b | done |
| ui.md | ## UI kit | contributing/web-ui.md | R10b | done |
| ui.md | ### Charts | contributing/web-ui.md | R10b | done |
| ui.md | ### DataList | contributing/web-ui.md | R10b | done |
| ui.md | ### RouteTabs | contributing/web-ui.md | R10b | done |
| ui.md | ### Truncated | contributing/web-ui.md | R10b | done |
| ui.md | ### Search palette | contributing/web-ui.md | R10b | done |
| ui.md | ### Sheets | contributing/web-ui.md | R10b | done |
| ui.md | ### Composer | contributing/web-ui.md | R10b | done |
| ui.md | ### Prompt card | contributing/web-ui.md | R10b | done |
| ui.md | ### Note thread | contributing/web-ui.md | R10b | done |
| ui.md | ### Milestone rows | contributing/web-ui.md | R10b | done |
| ui.md | ### Stream header | contributing/web-ui.md | R10b | done |
| ui.md | ### Inbox | contributing/web-ui.md | R10b | done |
| ui.md | ### Dashboard | contributing/web-ui.md | R10b | done |
| ui.md | ### Project Queue and Finishing | contributing/web-ui.md | R10b | done |
| ui.md | ### Plans | contributing/web-ui.md | R10b | done |
| ui.md | ### Milestone Issues | contributing/web-ui.md | R10b | done |
| ui.md | ### Milestone Pilot | contributing/web-ui.md | R10b | done |
| ui.md | ### Pause cards | contributing/web-ui.md | R10b | done |
| ui.md | ### Board | contributing/web-ui.md | R10b | done |
| ui.md | ### Milestone Retrospective | contributing/web-ui.md | R10b | done |
| ui.md | ### Spend tab | contributing/web-ui.md | R10b | done |
| ui.md | ### Research page | contributing/web-ui.md | R10b | done |
| ui.md | ### Research list and form | contributing/web-ui.md | R10b | done |
| ui.md | ## Roadmap | contributing/web-ui.md | R10b | done; updated: removed the shipped *Restart run* from the Actions item and "GitLab stays the place where the work is steered" |
| ui.md | ### Conventions check | contributing/web-ui.md | R10b | done |
| configuration.md | ## Installation | getting-started/install.md | R3 | done; updated: `helm upgrade --install` with a values file; "Without Helm" (`make deploy`, the agent service account) moved to the chart README "Without Helm" |
| configuration.md | ### Install with Flux | getting-started/install.md | R3 | done; updated: `ref.semver` in the example so it works as is |
| configuration.md | ### Web UI | getting-started/turn-on-the-desk.md | R3 | done; updated: "read-only" dropped; the reader login, env variables, retries and RBAC moved to research.md "Database" (R7b takes it to contributing/research.md) and the chart README |
| configuration.md | ### Research | getting-started/install.md | R3 | done; updated: target changed, see the steering log; setup in install.md "Add the database" with the chat writer login, operator flags, grants, backfill and the network policy in research.md "Database" |
| configuration.md | ### Plan | charts/ai-operator/README.md | R3 | done; updated: target changed, see the steering log; the table was already in the chart README "Plan", which now says how invalid values fail |
| configuration.md | ### Pricing | charts/ai-operator/README.md | R3 | done; updated: target changed, see the steering log; the values and the cost table were already in the chart README "Pricing", the cost sources and the lookup moved to contributing/design.md "Pricing" |
| configuration.md | ## Secrets | getting-started/install.md | R3 | done |
| configuration.md | ## `spec.gitlab` | reference/agentproject.md | R8b | done; updated: `project` and `tokenSecretRef` marked required |
| configuration.md | ## `spec.milestones` | reference/agentproject.md | R8b | done |
| configuration.md | ## `spec.autopilot` | reference/agentproject.md | R8b | done; updated: the warning text and `status.autopilot` fields moved to contributing/design.md "Autopilot" |
| configuration.md | ## `spec.houseRules` | reference/agentproject.md | R8b | done |
| configuration.md | ## `spec.workflow` | reference/agentproject.md | R8b | done; updated: the Conventional Commits pattern, the describer title rules and `spec.source.chatContext` moved to contributing/design.md |
| configuration.md | ### `spec.workflow.release` | reference/agentproject.md | R8b | done; updated: the checkpoint paragraph removed here (stated once in `spec.checkpoint`) |
| configuration.md | ## `spec.agents` | reference/agents-and-models.md | R8b | done; updated: `spec.agents` and `credentialsSecretName` marked required, `imagePullPolicy` (`IfNotPresent`) added, `logLevel` falls back to `info`, roles summarised in one table |
| configuration.md | ### Roles | reference/agents-and-models.md | R8b | done; updated: effort values listed with `xhigh`, the step-size gate hook moved to contributing/design.md "Token economy"; the conflict role's `engine` defaults to `claude` and does not inherit a cursor implementer |
| configuration.md | ### Subagents | reference/agents-and-models.md | R8b | done; updated: name, tools and required fields from the CRD; prompt wording and `--agents` moved to contributing/design.md "Token economy" |
| configuration.md | ### Read gate | reference/agents-and-models.md | R8b | done; updated: hook, messages and timeout moved to contributing/design.md "Token economy" |
| configuration.md | ### Per-issue model and effort | reference/agents-and-models.md | R8b | done; updated: the default `models` list stated, and that `models: []` allows only the profiles' and `implement`'s models |
| configuration.md | ### Profiles | reference/agents-and-models.md | R8b | done |
| configuration.md | ### Fallback engine | reference/agents-and-models.md | R8b | done; updated: the run report lines moved to contributing/design.md "Engines, profiles and subscriptions" |
| configuration.md | ### Engines | reference/agents-and-models.md | R8b | done; updated: CLI flags moved to contributing/design.md "Engines, profiles and subscriptions"; the table lists what each engine supports |
| configuration.md | ## `spec.lifecycle` | reference/lifecycle-and-ci.md | R8b | done; updated: stall detection links the watchdog and restart instead of the design page |
| configuration.md | ### Remote git timeouts | reference/lifecycle-and-ci.md | R8b | done |
| configuration.md | ## `spec.ci` | reference/lifecycle-and-ci.md | R8b | done; updated: per-milestone rule overrides linked |
| configuration.md | ## `spec.checkpoint` | reference/lifecycle-and-ci.md | R8b | done; updated: trailer, `status.run` fields and the PVC check moved to contributing/design.md "Checkpoints and Conventional Commits" |
| configuration.md | ## `spec.mainHealth` | reference/lifecycle-and-ci.md | R8b | done; updated: API call counts moved to contributing/design.md "Main health" |
| configuration.md | ## `spec.notify` | reference/agentproject.md | R8b | done; updated: default owners named by the chart value `defaultOwners` |
| configuration.md | ## `spec.pilot` | reference/agentproject.md | R8b | done; updated: the managed actions in plain words, `maxInterventionsPerIssue` at least 1; the spend source moved to contributing/design.md "Autopilot" |
| configuration.md | ## `spec.budget` | reference/agentproject.md | R8b | done; updated: `spec.budgetStop` and the status fields moved to contributing/design.md "Autopilot" |
| configuration.md | ## `spec.webhook` | reference/agentproject.md | R8b | done; updated: chart values linked by section |
| configuration.md | ## `spec.crossRepo` | reference/agentproject.md | R8b | done; updated: the cluster flag table replaced by the chart values (`crossRepo.*`, `selfProject`), the build version moved to contributing/design.md "Cross-repo" |
| configuration.md | ## Other `spec` fields | reference/agentproject.md | R8b | done; updated: `maxConcurrentRuns` at least 1 |
| configuration.md | ## Agent image | reference/agent-image.md | R8b | done; updated: no source file names, the build arguments kept |
| configuration.md | ## Network policy | reference/agent-image.md | R8b | done |
| commands.md | ## How commands work | reference/commands.md | R8a | done; updated: first-line-only parsing for routing commands (`review` must be the whole comment), case-insensitive words and optional `#`, Desk first |
| commands.md | ## On an issue or its merge request | reference/commands.md | R8a | done; updated: `use`, `effort`, `budget`, `repo` grouped as no-run commands |
| commands.md | ### Restart a stuck run (Desk chat) | reference/commands.md | R8a | done; updated: `/agent restart` stated as no GitLab command, heading now "Restart a stuck run" |
| commands.md | ### Model and effort | reference/commands.md | R8a | done |
| commands.md | ## On a closed issue | reference/commands.md | R8a | done |
| commands.md | ## On the tracking issue (autopilot) | reference/commands.md | R8a | done; updated: heading now "On the tracking issue", `create` replies moved to their own subsection, Desk first, numbers work without `#`, "owners only also with `commandsFrom: all`" fixed to `members` (no `all` value) |
| commands.md | ## To the pilot | reference/commands.md | R8a | done; updated: on a milestone without owners anyone may ask, prompt-thread answers in their own subsection |
| commands.md | ## Replies to comments not acted on | reference/commands.md | R8a | done; updated: merged with the subsection of the same name under the issue commands |
| commands.md | ## In the milestone description | reference/commands.md | R8a | done; updated: rule switch ranges (0 to 5, true or false) added |
| commands.md | ## Labels you may set yourself | reference/labels.md | R8a | done; updated: `agent::draft`, failed runs split into needs-input and blocked, blocked reasons, operator-set labels (`ai-generated`, `ci::*`, `agent::companion`) added |
| research.md | ## Concepts | guides/research.md | R7b | done; updated: Desk first ("Read results in the Desk", with the fields of *New research*); setup is the complete "Turn it on" example with the writer role; the agent choice and usage sharing are under "Choose agents" |
| research.md | ## `AgentResearch` | reference/agentresearch.md | R7b | done; updated: `lastRun` is the start of the last run, not its end; the `Running` condition added; `Valid` is checked again every minute, for any reason, while it is `False` |
| research.md | ## `spec.research` on `AgentProject` | reference/agentresearch.md | R7b | done; updated: "checked again whenever the project changes" dropped, the writer Secret is not watched |
| research.md | ## Examples | guides/research.md | R7b | done; updated: the sample file names dropped (source refs); the candidates override shows only `spec.research` |
| research.md | ## Feed sources | guides/research.md | R7b | done; the address list, user agent and code names went to contributing/research.md "Feeds in detail" |
| research.md | ## Choosing agents | guides/research.md | R7b | done; the default candidates are in reference/agentresearch.md "Candidates", the window and reset rules in contributing/research.md "Choosing agents in detail" |
| research.md | ## Runs | guides/research.md | R7b | done; the pod name, start and end details and requeues went to contributing/research.md "Runs in detail"; updated: deleting a research removes its pod by owner reference (garbage collection), not by the controller |
| research.md | ## The agent's answer | guides/research.md | R7b | done; the user part (web only, one repair round, outcomes) is in the guide, the prompt and tool details in contributing/research.md |
| research.md | ## In the pod | contributing/research.md | R7b | done |
| research.md | ## Runs, results and new items | contributing/research.md | R7b | done; updated: retention deletes in batches of 1000 |
| research.md | ## Block schema v1 | contributing/research.md | R7b | done |
| research.md | ## Retention | guides/research.md | R7b | done; the internals are with "Runs, results and new items" in contributing/research.md |
| research.md | ## Database | contributing/research.md | R7b | done; updated: the chat writer row names `ui.chat.writerSecret`; the logins table is "Logins" (four, not three) |
| research.md | ### Example: my-cluster | contributing/research.md | R7b | done; updated: notes that the Desk chat also needs the chat writer login |
| research.md | ## Troubleshooting | guides/research.md | R7b | done; updated: `Valid` is checked again every minute only while it is `False` |
| design.md | ## Architecture | contributing/design.md | R10a | done; updated: opening describes autopilot, pilot and Desk |
| design.md | ## Flow | contributing/design.md | R10a | done |
| design.md | ## Custom resources | contributing/design.md | R10a | done |
| design.md | ## Pod topology | contributing/design.md | R10a | done |
| design.md | ### Stall detection | contributing/design.md | R10a | done |
| design.md | ## Lifecycle of long-lived issues | contributing/design.md | R10a | done |
| design.md | ### Desk live chat | contributing/design.md | R10a | done |
| design.md | ## Token economy | contributing/design.md | R10a | done |
| design.md | ## History | contributing/design.md | R10a | done |
| design.md | ### Research store migrations | contributing/design.md | R10a | done |
| design.md | ### Desk snapshot | contributing/design.md | R10a | done |
| design.md | ## GitLab webhooks | contributing/design.md | R10a | done |
| design.md | ## CI feedback | contributing/design.md | R10a | done |
| design.md | ### Main health | contributing/design.md | R10a | done |
| design.md | ## Checkpoints and Conventional Commits | contributing/design.md | R10a | done |
| design.md | ## Merge conflicts | contributing/design.md | R10a | done |
| design.md | ## Autopilot | contributing/design.md | R10a | done |
| design.md | ### Revisiting after the merge | contributing/design.md | R10a | done |
| design.md | ## Pilot | contributing/design.md | R10a | done |
| design.md | ### Prompts in GitLab | contributing/design.md | R10a | done |
| design.md | ### Answering prompts | contributing/design.md | R10a | done |
| design.md | ## Engines, profiles and subscriptions | contributing/design.md | R10a | done |
| design.md | ## Cross-repo | contributing/design.md | R10a | done |
| design.md | ## Security | contributing/design.md | R10a | done |
| design.md | ## Desk session model | contributing/design.md | R10a | done |
| design.md | ## Desk styles | contributing/design.md | R10a | done |
| design.md | ## Code map | contributing/design.md | R10a | done |
| design.md | ## Roadmap | contributing/design.md | R10a | done |
| releasing.md | ## Where the code lives | contributing/releasing.md | R10a | done |
| releasing.md | ## Versioning | contributing/releasing.md | R10a | done |
| releasing.md | ## GitHub releases | contributing/releasing.md | R10a | done |
| releasing.md | ## GitLab releases | contributing/releasing.md | R10a | done |
| releasing.md | ## First release checklist | contributing/releasing.md | R10a | done |
| releasing.md | ## Local builds | contributing/releasing.md | R10a | done |
| sandbox-test-plan.md | ## What this test is most likely to find | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ## 0. Prerequisites | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ## 1. Setup | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### 1.1 GitLab project | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### 1.2 Cluster and operator | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ## 2. Test cases | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T1 – Operator connects to GitLab | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T2 – Planning a milestone | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T3 – Re-planning | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T4 – Implementing a `fast` issue | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T5 – Implementing a `strong` issue, questions, resume | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T6 – Iterating on the MR | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T7 – No triggers from the bot or from chatter | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T8 – Queueing | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T9 – Lifecycle: idle, hibernate, archive, wake up | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T10 – Finishing an issue | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T11 – Run limit | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T12 – Failure handling | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T13 – Security: credential isolation | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T14 – Network policy (optional) | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T15 – Helm upgrade and uninstall | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T16 – Usage limits | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T17 – CI feedback loop | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ## 3. When something fails | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T18 – Autopilot milestone | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ### T19 – Merge conflicts | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ## 4. Results | contributing/sandbox-test-plan.md | R10a | done |
| sandbox-test-plan.md | ## 5. Cleanup | contributing/sandbox-test-plan.md | R10a | done |

## Steering log

Narrowed requirements, follow-up issues and changes of plan, one line each,
with the requirement label and the issue or merge request. Issues in the
order they landed: R1 #503, R2 #504, R11 #505, R10a #506, R10b #507, R9
#508, R8a #509, R8b #510, R3 #511, R4 #512, R5 #513, R6 #514, R7a #515, R7b
#516, R12 #517, R13 #518, R14 #519.

- R1 (#503): inventory of every old `##`/`###` at 90824ec, empty section
  folders with README indexes, and `docs/README.md` as the map (Decision 1).
  No pages moved yet; old top-level files stayed until their MRs.
- R2 (#504): `internal/docscheck` with the standard library only. Cap and
  source-ref rules bind the four user folders from the start; table tests
  cover broken links, missing anchors, 401-line pages, Go paths in guides,
  unresolved `docs/….md#…` refs in code, and slug edge cases. The rules
  section of this spec gained the "docs check" bullet.
- R11 (#505): `docs/specs/README.md` lists every spec with one line and its
  milestone; specs record their time and the docs win when they disagree
  (Decision 7). Specs themselves were not rewritten.
- R10a (#506): `design.md`, `releasing.md` and `sandbox-test-plan.md` moved
  to `contributing/` with link fixes in `.gitlab-ci.yml`, `Makefile`,
  `CLAUDE.md`, `web/` and `hack/`. Opening of `design.md` updated for
  autopilot, the pilot and the Desk (Decision 6).
- R10b (#507): contributor parts of `ui.md` became
  `contributing/web-ui.md`; user parts stayed in `ui.md` for R6/R7a.
- R9 (#508): the four concept pages are new text; no inventory row is only a
  concept explanation, so none moved. Glossary entries name pages that do
  not exist yet in plain text; the MRs that add them turn those into links.
- R8a (#509): `reference/labels.md` also describes the `agent::` state labels
  and their moves, checked against the code. The overview in `usage.md`'s
  "One-time setup in GitLab" stays until R3 moves that row, which then links
  `reference/labels.md` instead of repeating it.
- R8b (#510): the `AgentProject` reference is split into four pages (plan
  decision D510-P1): `reference/agentproject.md`,
  `reference/agents-and-models.md` (target of the `spec.agents` rows, planned
  as `reference/agents.md`), `reference/lifecycle-and-ci.md` (`lifecycle`,
  `ci`, `checkpoint`, `mainHealth`) and `reference/agent-image.md` (agent
  image, network policy). Internals of these sections and the Desk API's
  answer shapes moved to `contributing/design.md`. The CRD requires
  `spec.agents.credentialsSecretName`; the old tables did not say so.
- R3 (#511): `docs/configuration.md` is deleted, so its Research, Plan and
  Pricing rows moved here instead of with R7a and R7b. Research setup is the
  "Add the database" section of `getting-started/install.md`; it is not
  optional on the getting-started path, since answering a prompt in the
  Desk needs the database and the chat writer login. Plan and Pricing were
  already in the chart README, the Helm values reference, so they stay
  there once; the internals went to `research.md` "Database" (moved by R7b)
  and `contributing/design.md` "Pricing". The planner asks no questions on
  autopilot, so `getting-started/first-milestone.md` answers a revisit
  prompt on a planner decision.
- R4 (#512): owners and pings moved to their own page,
  `guides/owners-and-pings.md` (plan decision D512-P1), since main health
  made `guides/while-the-agent-works.md` long and owners are set once per
  milestone. The R4 guides still link `usage.md` for autopilot, pauses, the
  pilot and watching from Kubernetes until R5 and R7b move them.
  `reference/lifecycle-and-ci.md` said merged-results pipelines are not
  followed; the code follows them, so it was fixed with the CI feedback
  row. The Desk's Revisit opens a prompt only once the decision's issue is
  merged, so `getting-started/first-milestone.md` now shows the first merge
  request before it answers the revisit prompt.
- R5 (#513): the autopilot area became four guides. Talking to the pilot
  went to its own `guides/pilot.md` (plan decision D513-P1), and cut-off
  runs, usage limits, issues that stay open and agent notes went to
  `guides/when-runs-go-wrong.md` (D513-P2), so `guides/autopilot.md` keeps
  the normal path. `TestPauseKindsDocumented` reads `guides/pauses.md`.
  `usage.md` keeps budgets, companions and releases (R7a) under a new
  heading, "Budgets, companions and releases", until they move. The old
  tracking-issue table lacked `/agent release` and `/agent no-release`; the
  guide's table has them and links `reference/commands.md` for the rest.
  The pause records (`resolvedBy`, `via`), the retrospective's facts and
  what the pilot's state lists were not in `contributing/design.md` yet,
  so they moved there.
- R6 (#514): the user parts of `ui.md` and `usage.md`'s Prompts and
  Watching progress became `guides/desk.md` (what you see) and
  `guides/steer-from-the-desk.md` (what you do). Queue and Finishing and
  the Planner went to the steering guide, the Live transcript to the Desk
  guide. Their internals (URLs, event kinds, command bodies, fallback pause
  actions, limits, the security mechanics) moved to
  `contributing/web-ui.md` "Desk behaviour in detail" (plan decision
  D514-P1 for Security), the prompt kinds and lifecycle internals to
  `contributing/design.md` "Prompts in GitLab". `ui.md` keeps only Spend
  and the Costs page for R7a; "Costs at a glance" links them until the
  budgets guide exists. The old text placed engine usage on the Board tab;
  it is in the rail, the *Working now* cards and the Costs page.
- R7a (#515): `guides/releases.md`, `guides/budgets-and-spend.md` and
  `guides/cross-repo-changes.md` replace usage.md's budgets, companions and
  releases and the rest of `ui.md`, which is deleted. The code comments on
  releases now name `docs/guides/releases.md`, so the CRD descriptions and
  `web/src/generated/` changed here, not in R12 (plan decision D515-P1). The
  spend report SQL moved to `contributing/design.md` "Spend report script"
  (D515-P2). Release internals are a new "Releases" subsection under
  "Autopilot", budget internals "Budgets in detail", companion internals
  "Companions in detail" (only what "Cross-repo" lacked), and the Spend tab
  and Costs page internals are in `contributing/web-ui.md`. `usage.md`
  keeps only watching from Kubernetes (R7b).
- R7b (#516): `guides/watch-from-kubernetes.md`, `guides/research.md`,
  `reference/agentresearch.md` and `contributing/research.md` (plan
  decision D516-P1) replace the rest of `usage.md` and all of
  `research.md`; both are deleted, so no old top-level page is left and
  `docs/README.md` lost its "Pages being moved" section. The research
  internals that were spread over the old sections (pod name, run end,
  requeues, the agent choice's window rules, the feed fetch guard, the
  prompt and tools) are grouped under "… in detail" headings in
  `contributing/research.md`. The code comment on `spec.research` now
  names `docs/guides/research.md`, so the CRD descriptions and
  `web/src/generated/` changed here too, like in R7a. The glossary gained
  a "Research" entry. The old run step list put *open merge request* after
  the push; the runner opens the draft before the agent starts.
- R12 (#517): the MRs that emptied the old pages deleted them and fixed
  their refs, the chart README links and its web UI section (plan decision
  D517-P1), so this MR only sweeps. The pilot tab's "read-only" (no grant)
  now reads "see but cannot write", and `guides/pilot.md` names the
  `steer`-only case. `docs/README.md` names every page. The plan caps, idle
  time and retention, `research.historyRetention` and a research's
  `retention` were stated as numbers in guides; they now link the chart
  README and `reference/agentresearch.md`. On/off defaults said in passing
  ("empty by default", "the default agents run on claude") stay, each next
  to its reference link. The README's "move cards" tagline and row are
  R13's.
  The glossary's Desk and Prompt entries still named the Desk guides as
  planned plain text (R9's note); they are links now.
- R13 (#518): the README leads with autopilot, the pilot and the Desk.
  The label hand-over is one sentence that links
  `guides/answer-and-hand-over.md`. "Talk to it in GitLab" became "Steer it"
  (`#steer-it`); `#quick-start` stays. Highlights went from 13 to 12 rows:
  "Describes its work" joined "Implements and reviews", "Iterates by
  comment" became the chat row, "Planned releases" joined "Autopilot",
  "Right model for the job" joined "Easy on tokens", and the Desk row left
  because "How it works" and "Steer it" cover it. New rows: the pilot, spend
  and budgets, chat, and hung runs. `/agent restart` is no GitLab command
  (restarting is a Desk action, see `reference/commands.md`), so "Steer it"
  names it under the Desk and says so instead of listing it as a comment.
  The defaults sentence was checked against the CRD defaults: it now
  names the pilot on Sonnet and the reviewers by name. The Quick start
  installs from a values file, so the Desk step adds its `ui` block to the
  same file, and it drops the board, which only helps the manual flow.
- R14 (#519): `CLAUDE.md` gained a short docs rule (layout, plain language,
  400-line cap and no source refs, folder and specs indexes, chart README).
  The `milestone-spec` skill and template name Docs pages by the new layout
  and require listing the spec in `docs/specs/README.md`. Links already
  pointed at `docs/reference/commands.md` and
  `docs/guides/write-a-milestone.md`. This log now has a line for every
  requirement R1 to R14.

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
| usage.md | ## One-time setup in GitLab | getting-started/install.md | R3 | open |
| usage.md | ### Webhooks (optional) | getting-started/install.md | R3 | open |
| usage.md | ## 1. Write the milestone spec | guides/write-a-milestone.md | R4 | open |
| usage.md | ### Dependencies as issue links | guides/write-a-milestone.md | R4 | open |
| usage.md | ## 2. Answer questions and hand the issue over | guides/answer-and-hand-over.md | R4 | open |
| usage.md | ## 3. The agent works | guides/while-the-agent-works.md | R4 | open |
| usage.md | ### Commit messages | guides/while-the-agent-works.md | R4 | open |
| usage.md | ### Toolchains the agent installs | guides/while-the-agent-works.md | R4 | open |
| usage.md | ### Owners and pings | guides/while-the-agent-works.md | R4 | open |
| usage.md | ## CI feedback | guides/while-the-agent-works.md | R4 | open |
| usage.md | ## Main health | guides/while-the-agent-works.md | R4 | open |
| usage.md | ## Merge conflicts | guides/while-the-agent-works.md | R4 | open |
| usage.md | ## 4. Iterate | guides/iterate-and-finish.md | R4 | open |
| usage.md | ### Decisions | guides/iterate-and-finish.md | R4 | open |
| usage.md | ## 5. Finish | guides/iterate-and-finish.md | R4 | open |
| usage.md | ## Autopilot: a milestone end to end | guides/autopilot.md | R5 | open |
| usage.md | ### Several milestones at once | guides/autopilot.md | R5 | open |
| usage.md | ### The merge gate | guides/autopilot.md | R5 | open |
| usage.md | ### When it pauses | guides/pauses.md | R5 | open |
| usage.md | ### Budget | guides/budgets-and-spend.md | R7a | open |
| usage.md | ### Companion repository | guides/cross-repo-changes.md | R7a | open |
| usage.md | ### Releases | guides/releases.md | R7a | open |
| usage.md | ### Commands on the tracking issue | guides/autopilot.md | R5 | open |
| usage.md | ### Tracking issue | guides/autopilot.md | R5 | open |
| usage.md | ### Switching it off | guides/autopilot.md | R5 | open |
| usage.md | ## Prompts | guides/steer-from-the-desk.md | R6 | open |
| usage.md | ## Talking to the pilot | guides/pilot.md | R5 | open |
| usage.md | ### Spec changes through the pilot | guides/pilot.md | R5 | open |
| usage.md | ### The retrospective | guides/pilot.md | R5 | open |
| usage.md | ## When a run is cut off | guides/autopilot.md | R5 | open |
| usage.md | ## Usage and rate limits | guides/autopilot.md | R5 | open |
| usage.md | ## Issues that stay open | guides/autopilot.md | R5 | open |
| usage.md | ### Agent notes | guides/autopilot.md | R5 | open |
| usage.md | ## Watching progress | guides/desk.md | R6 | open |
| usage.md | ## Watching from Kubernetes | guides/watch-from-kubernetes.md | R7b | open |
| usage.md | ### Run logs | guides/watch-from-kubernetes.md | R7b | open |
| usage.md | ### When the agent container dies | guides/watch-from-kubernetes.md | R7b | open |
| usage.md | ### When the whole pod is lost | guides/watch-from-kubernetes.md | R7b | open |
| ui.md | ## Screenshots | guides/desk.md | R6 | open |
| ui.md | ## Desk and history | guides/desk.md | R6 | open |
| ui.md | ### Navigation | guides/desk.md | R6 | open |
| ui.md | ### Queue and Finishing | guides/desk.md | R6 | open |
| ui.md | ### Planner | guides/desk.md | R6 | open |
| ui.md | ### Spend | guides/budgets-and-spend.md | R7a | open |
| ui.md | ### Costs page | guides/budgets-and-spend.md | R7a | open |
| ui.md | ### Agent calls on the Task page | guides/desk.md | R6 | open |
| ui.md | ### Notifications | guides/desk.md | R6 | open |
| ui.md | ## Platform | guides/desk.md | R6 | open |
| ui.md | ## What it shows | guides/desk.md | R6 | open |
| ui.md | ## Enable it | getting-started/turn-on-the-desk.md | R3 | open |
| ui.md | ### 1. Create the OIDC client | getting-started/turn-on-the-desk.md | R3 | open |
| ui.md | ### 2. Create the Secret | getting-started/turn-on-the-desk.md | R3 | open |
| ui.md | ### 3. Set the chart values | getting-started/turn-on-the-desk.md | R3 | open |
| ui.md | ### 4. Staying logged in | getting-started/turn-on-the-desk.md | R3 | open |
| ui.md | ## Access rules | guides/desk.md | R6 | open |
| ui.md | ### Research rules | guides/desk.md | R6 | open |
| ui.md | ### Claim mapping | guides/desk.md | R6 | open |
| ui.md | ## Live transcript | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ## Steering | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ### Set it up | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ### One user, a fixed token | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ### How it works | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ## Without login | getting-started/turn-on-the-desk.md | R3 | open |
| ui.md | ## Security | guides/desk.md | R6 | open |
| ui.md | ## API | reference/desk-api.md | R8b | open |
| ui.md | ## Development | contributing/web-ui.md | R10b | open |
| ui.md | ## Checks | contributing/web-ui.md | R10b | open |
| ui.md | ### Docs screenshots | contributing/web-ui.md | R10b | open |
| ui.md | ## Bundle budget | contributing/web-ui.md | R10b | open |
| ui.md | ## UI kit | contributing/web-ui.md | R10b | open |
| ui.md | ### Charts | contributing/web-ui.md | R10b | open |
| ui.md | ### DataList | contributing/web-ui.md | R10b | open |
| ui.md | ### RouteTabs | contributing/web-ui.md | R10b | open |
| ui.md | ### Truncated | contributing/web-ui.md | R10b | open |
| ui.md | ### Search palette | contributing/web-ui.md | R10b | open |
| ui.md | ### Sheets | contributing/web-ui.md | R10b | open |
| ui.md | ### Composer | contributing/web-ui.md | R10b | open |
| ui.md | ### Prompt card | contributing/web-ui.md | R10b | open |
| ui.md | ### Note thread | contributing/web-ui.md | R10b | open |
| ui.md | ### Milestone rows | contributing/web-ui.md | R10b | open |
| ui.md | ### Stream header | contributing/web-ui.md | R10b | open |
| ui.md | ### Inbox | contributing/web-ui.md | R10b | open |
| ui.md | ### Dashboard | contributing/web-ui.md | R10b | open |
| ui.md | ### Project Queue and Finishing | contributing/web-ui.md | R10b | open |
| ui.md | ### Plans | contributing/web-ui.md | R10b | open |
| ui.md | ### Milestone Issues | contributing/web-ui.md | R10b | open |
| ui.md | ### Milestone Pilot | contributing/web-ui.md | R10b | open |
| ui.md | ### Pause cards | contributing/web-ui.md | R10b | open |
| ui.md | ### Board | contributing/web-ui.md | R10b | open |
| ui.md | ### Milestone Retrospective | contributing/web-ui.md | R10b | open |
| ui.md | ### Spend tab | contributing/web-ui.md | R10b | open |
| ui.md | ### Research page | contributing/web-ui.md | R10b | open |
| ui.md | ### Research list and form | contributing/web-ui.md | R10b | open |
| ui.md | ## Roadmap | contributing/web-ui.md | R10b | open |
| ui.md | ### Conventions check | contributing/web-ui.md | R10b | open |
| configuration.md | ## Installation | getting-started/install.md | R3 | open |
| configuration.md | ### Install with Flux | getting-started/install.md | R3 | open |
| configuration.md | ### Web UI | getting-started/turn-on-the-desk.md | R3 | open |
| configuration.md | ### Research | guides/research.md | R7b | open |
| configuration.md | ### Plan | getting-started/turn-on-the-desk.md | R3 | open |
| configuration.md | ### Pricing | guides/budgets-and-spend.md | R7a | open |
| configuration.md | ## Secrets | getting-started/install.md | R3 | open |
| configuration.md | ## `spec.gitlab` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.milestones` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.autopilot` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.houseRules` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.workflow` | reference/agentproject.md | R8b | open |
| configuration.md | ### `spec.workflow.release` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.agents` | reference/agents.md | R8b | open |
| configuration.md | ### Roles | reference/agents.md | R8b | open |
| configuration.md | ### Subagents | reference/agents.md | R8b | open |
| configuration.md | ### Read gate | reference/agents.md | R8b | open |
| configuration.md | ### Per-issue model and effort | reference/agents.md | R8b | open |
| configuration.md | ### Profiles | reference/agents.md | R8b | open |
| configuration.md | ### Fallback engine | reference/agents.md | R8b | open |
| configuration.md | ### Engines | reference/agents.md | R8b | open |
| configuration.md | ## `spec.lifecycle` | reference/agentproject.md | R8b | open |
| configuration.md | ### Remote git timeouts | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.ci` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.checkpoint` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.mainHealth` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.notify` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.pilot` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.budget` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.webhook` | reference/agentproject.md | R8b | open |
| configuration.md | ## `spec.crossRepo` | reference/agentproject.md | R8b | open |
| configuration.md | ## Other `spec` fields | reference/agentproject.md | R8b | open |
| configuration.md | ## Agent image | reference/agentproject.md | R8b | open |
| configuration.md | ## Network policy | reference/agentproject.md | R8b | open |
| commands.md | ## How commands work | reference/commands.md | R8a | open |
| commands.md | ## On an issue or its merge request | reference/commands.md | R8a | open |
| commands.md | ### Restart a stuck run (Desk chat) | reference/commands.md | R8a | open |
| commands.md | ### Model and effort | reference/commands.md | R8a | open |
| commands.md | ## On a closed issue | reference/commands.md | R8a | open |
| commands.md | ## On the tracking issue (autopilot) | reference/commands.md | R8a | open |
| commands.md | ## To the pilot | reference/commands.md | R8a | open |
| commands.md | ## Replies to comments not acted on | reference/commands.md | R8a | open |
| commands.md | ## In the milestone description | reference/commands.md | R8a | open |
| commands.md | ## Labels you may set yourself | reference/labels.md | R8a | open |
| research.md | ## Concepts | guides/research.md | R7b | open |
| research.md | ## `AgentResearch` | reference/agentresearch.md | R7b | open |
| research.md | ## `spec.research` on `AgentProject` | reference/agentresearch.md | R7b | open |
| research.md | ## Examples | guides/research.md | R7b | open |
| research.md | ## Feed sources | guides/research.md | R7b | open |
| research.md | ## Choosing agents | guides/research.md | R7b | open |
| research.md | ## Runs | guides/research.md | R7b | open |
| research.md | ## The agent's answer | guides/research.md | R7b | open |
| research.md | ## In the pod | contributing/research.md | R7b | open |
| research.md | ## Runs, results and new items | contributing/research.md | R7b | open |
| research.md | ## Block schema v1 | contributing/research.md | R7b | open |
| research.md | ## Retention | guides/research.md | R7b | open |
| research.md | ## Database | contributing/research.md | R7b | open |
| research.md | ### Example: my-cluster | contributing/research.md | R7b | open |
| research.md | ## Troubleshooting | guides/research.md | R7b | open |
| design.md | ## Architecture | contributing/design.md | R10a | open |
| design.md | ## Flow | contributing/design.md | R10a | open |
| design.md | ## Custom resources | contributing/design.md | R10a | open |
| design.md | ## Pod topology | contributing/design.md | R10a | open |
| design.md | ### Stall detection | contributing/design.md | R10a | open |
| design.md | ## Lifecycle of long-lived issues | contributing/design.md | R10a | open |
| design.md | ### Desk live chat | contributing/design.md | R10a | open |
| design.md | ## Token economy | contributing/design.md | R10a | open |
| design.md | ## History | contributing/design.md | R10a | open |
| design.md | ### Research store migrations | contributing/design.md | R10a | open |
| design.md | ### Desk snapshot | contributing/design.md | R10a | open |
| design.md | ## GitLab webhooks | contributing/design.md | R10a | open |
| design.md | ## CI feedback | contributing/design.md | R10a | open |
| design.md | ### Main health | contributing/design.md | R10a | open |
| design.md | ## Checkpoints and Conventional Commits | contributing/design.md | R10a | open |
| design.md | ## Merge conflicts | contributing/design.md | R10a | open |
| design.md | ## Autopilot | contributing/design.md | R10a | open |
| design.md | ### Revisiting after the merge | contributing/design.md | R10a | open |
| design.md | ## Pilot | contributing/design.md | R10a | open |
| design.md | ### Prompts in GitLab | contributing/design.md | R10a | open |
| design.md | ### Answering prompts | contributing/design.md | R10a | open |
| design.md | ## Engines, profiles and subscriptions | contributing/design.md | R10a | open |
| design.md | ## Cross-repo | contributing/design.md | R10a | open |
| design.md | ## Security | contributing/design.md | R10a | open |
| design.md | ## Desk session model | contributing/design.md | R10a | open |
| design.md | ## Desk styles | contributing/design.md | R10a | open |
| design.md | ## Code map | contributing/design.md | R10a | open |
| design.md | ## Roadmap | contributing/design.md | R10a | open |
| releasing.md | ## Where the code lives | contributing/releasing.md | R10a | open |
| releasing.md | ## Versioning | contributing/releasing.md | R10a | open |
| releasing.md | ## GitHub releases | contributing/releasing.md | R10a | open |
| releasing.md | ## GitLab releases | contributing/releasing.md | R10a | open |
| releasing.md | ## First release checklist | contributing/releasing.md | R10a | open |
| releasing.md | ## Local builds | contributing/releasing.md | R10a | open |
| sandbox-test-plan.md | ## What this test is most likely to find | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ## 0. Prerequisites | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ## 1. Setup | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### 1.1 GitLab project | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### 1.2 Cluster and operator | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ## 2. Test cases | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T1 – Operator connects to GitLab | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T2 – Planning a milestone | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T3 – Re-planning | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T4 – Implementing a `fast` issue | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T5 – Implementing a `strong` issue, questions, resume | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T6 – Iterating on the MR | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T7 – No triggers from the bot or from chatter | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T8 – Queueing | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T9 – Lifecycle: idle, hibernate, archive, wake up | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T10 – Finishing an issue | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T11 – Run limit | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T12 – Failure handling | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T13 – Security: credential isolation | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T14 – Network policy (optional) | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T15 – Helm upgrade and uninstall | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T16 – Usage limits | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T17 – CI feedback loop | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ## 3. When something fails | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T18 – Autopilot milestone | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ### T19 – Merge conflicts | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ## 4. Results | contributing/sandbox-test-plan.md | R10a | open |
| sandbox-test-plan.md | ## 5. Cleanup | contributing/sandbox-test-plan.md | R10a | open |

## Steering log

Narrowed requirements, follow-up issues and changes of plan, one line each,
with the requirement label and the issue or merge request.

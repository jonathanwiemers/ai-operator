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
| ui.md | ## Enable it | getting-started/turn-on-the-desk.md | R3 | done |
| ui.md | ### 1. Create the OIDC client | getting-started/turn-on-the-desk.md | R3 | done; updated: the Refresh Token grant type named in the Zitadel steps; the username left at its default (email) |
| ui.md | ### 2. Create the Secret | getting-started/turn-on-the-desk.md | R3 | done |
| ui.md | ### 3. Set the chart values | getting-started/turn-on-the-desk.md | R3 | done; updated: the rule grants `chat` and the chat writer login is set, which answering prompts needs, and `spec.agents.chat` is turned on in the project |
| ui.md | ### 4. Staying logged in | getting-started/turn-on-the-desk.md | R3 | done; updated: defaults left to the chart README; renewal internals are in contributing/design.md "Desk session model" |
| ui.md | ## Access rules | guides/desk.md | R6 | open |
| ui.md | ### Research rules | guides/desk.md | R6 | open |
| ui.md | ### Claim mapping | guides/desk.md | R6 | open |
| ui.md | ## Live transcript | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ## Steering | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ### Set it up | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ### One user, a fixed token | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ### How it works | guides/steer-from-the-desk.md | R6 | open |
| ui.md | ## Without login | getting-started/turn-on-the-desk.md | R3 | done; updated: the user name is `anonymous`; the DNS rebinding detail stays in ui.md "Security" (R6) |
| ui.md | ## Security | guides/desk.md | R6 | open |
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
with the requirement label and the issue or merge request.

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

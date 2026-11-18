/agent spec  
/agent autopilot  
/agent owners @jonathan.wiemers  
/agent repo jo.wiemers/infra/cluster merge  

# Projects: the operator works in every project of a GitLab group, except the ones you exclude

## Goal

1. One `AgentProjectSet` with `include: ["jo.wiemers/**"]` makes the operator
   work in every project of the group and its subgroups: no hand-written
   AgentProject per repository.
2. An `exclude` list keeps chosen projects out, whatever `include` says.
3. Watching 100+ projects costs the bot about one GitLab request per minute
   while they are idle, not one sync per project per poll.
4. A project gets its own AgentProject only while it has work for the
   agents: a milestone with the spec marker, an issue labelled with the
   ready label, or a task still open.

## Background

File refs are at origin/main 645f0e4, 2026-11-16. This milestone runs after
"Desk Decisions tab" (%28) by milestone order and needs nothing from it.

What exists:

- **One AgentProject per repository.** `AgentProjectSpec.GitLab.Project`
  (`api/v1alpha1/agentproject_types.go:110`) names one project. Infra writes
  13 of them by hand plus the ai-operator one
  (`clusters/netcup01/applications/ai-operator/projects/agentprojects.yaml`);
  `defaults.yaml` gives all of them the token `gitlab/token`, the spec marker
  `/agent spec`, `pollInterval: 2m` and `maxConcurrentRuns: 1`.
- **Polling per project.** Each poll is a full sync (`sync`,
  `internal/controller/agentproject_controller.go:314`) unless webhook
  deliveries arrive; then a light poll lists the milestones
  (`milestonesChanged`, `:231`, one `ListMilestones` per project) and full
  syncs run every `webhook.fallbackInterval` (`fullSyncDue`, `:206`).
  Infra configures no webhook, so all 14 projects full-sync every 2 minutes.
  The client counts requests per poll (`gitlab.Stats`,
  `internal/gitlab/client.go:506`) but only logs them at V(1).
- **Webhooks.** `syncWebhook` (`internal/controller/webhook.go:33`) registers
  one project hook when `webhook.enabled`; the receiver finds the
  AgentProject by `spec.gitlab.project` (`internal/webhook/receiver.go:83`),
  records the delivery (`internal/webhook/deliveries.go`) and wakes the
  project (`internal/webhook/waker.go`). GitLab Free has no group hooks.
- **Deleting an AgentProject** has no finalizer: its AgentTasks go with it
  (controller reference, `agentproject_controller.go:492`) and its GitLab
  hook stays behind.
- **Cross-repo.** `ValidateCrossRepo` (`api/v1alpha1/crossrepo.go:85`)
  checks targets against `--cross-repo-allowed-projects` and
  `--cross-repo-never-projects` (`cmd/operator/main.go:49,51`), both exact
  paths.
- **Rate limits, measured 2026-11-16 on gitlab.com.** The bot has 100
  requests per minute (`burst_traffic_per_user_free_plan`), per user. One
  `/api/graphql` POST counts 1 however much it reads; a multiplexed batch
  also counts 1. The complexity cap is 250. This query scores 52:
  `group(fullPath:"jo.wiemers"){projects(includeSubgroups:true, first:100){nodes{fullPath archived milestones(state:active, first:20){nodes{iid title updatedAt}}}}}`.
  With milestone `description` and the group's ready issues added (the
  query of Decision 3) it scores 68. jo.wiemers has 112 projects with
  `archived/`, so two pages.
- **Desk** lists AgentProjects at `GET /api/v1/projects`
  (`internal/ui/server.go`); routes are keyed by namespace and name.

## Decisions (locked)

1. **New CRD `AgentProjectSet`** (namespaced, `ai.wiemers.me/v1alpha1`):
   `gitlab.url`, `gitlab.tokenSecretRef`, `gitlab.group` (the root group
   path), `include` and `exclude` (path globs, `*` within one path segment,
   `**` across segments), `template`, `pollInterval` (default 2m) and
   `dormantAfter` (default 168h). `template` holds every `AgentProjectSpec`
   field except `gitlab`; it is stored schemaless
   (`+kubebuilder:validation:Schemaless`,
   `+kubebuilder:pruning:PreserveUnknownFields`, type object), because
   `AgentProjectSpec` requires `gitlab.project` and `tokenSecretRef`. The
   reconciler decodes it strictly (unknown fields refused) into an
   `AgentProjectSpec` and sets `gitlab` from the set's `url` and
   `tokenSecretRef` plus the project path; `template.gitlab` present or a
   decode error makes `Ready=False`, reason `InvalidTemplate`. The API
   server defaults the children as usual. Rejected: a glob in
   `AgentProject.spec.gitlab.project` (one object would own many projects'
   tasks, Desk routes and hooks); a copied template type (100+ fields kept
   in sync by hand).
2. **Exclude beats include.** Archived projects are always skipped. A
   project that an AgentProject not owned by the set already watches (same
   `spec.gitlab.project`, case-insensitive, any namespace) is skipped too:
   the explicit one wins and keeps its settings.
3. **Discovery is GraphQL, one POST per 100 projects.** Each set poll asks
   for the group's projects (`includeSubgroups: true`, `fullPath`,
   `archived`, active milestones with `iid`, `title`, `description`,
   `updatedAt`, `first: 100` projects and `first: 20` milestones), and in
   the same POST the group's open issues with the template's
   `workflow.readyLabel` (default `agent::ready`; `includeSubgroups: true`,
   `reference(full: true)`, `first: 100`) and
   `queryComplexity { score limit }`. Both connections follow `pageInfo`;
   a later POST carries only the connections that still have pages. Rejected:
   REST per project (100+ requests per poll); group webhooks (Premium).
4. **A project is active** while it has an active milestone whose
   description contains the template's spec marker, an open issue with the
   ready label, or an AgentTask of its child. Only active projects get a child
   AgentProject. A child that has been inactive for `dormantAfter` and has
   no AgentTask is removed: the set first switches its webhook off, waits
   until the hook is gone (`WebhookRegistered` absent), then deletes it.
   An issue `/agent` comment in a project without a child is not seen;
   label the issue with the ready label or add a milestone.
5. **Children** are AgentProjects in the set's namespace, named
   `naming.Slug` of the path below `gitlab.group` (`devops/functions/pnpm`
   becomes `devops-functions-pnpm`), cut to 50 characters plus a 6-character
   hash of the full path when longer. A name taken by an object the set
   does not own skips the project and says so in the set's status. The set
   owns its children (controller reference, label
   `ai.wiemers.me/project-set`), and changes to the template reach them on
   the next set poll.
6. **Set children need webhooks.** The set refuses a template without
   `webhook.enabled` and `webhook.secretRef` (condition `Ready=False`,
   reason `WebhookRequired`). A set child does not poll GitLab on its
   own between full syncs: it skips the light poll, and counts as
   receiving deliveries while the set's `lastDiscoveryTime` is younger
   than twice the set's `pollInterval`, so it full-syncs only on a
   delivery, on a set wake and every `webhook.fallbackInterval`. When the
   set sees a child's milestones change (iid, title or `updatedAt`), it
   records a delivery for the child and wakes it through the delivery
   store and waker the receiver uses (`internal/webhook`). When the set's
   discovery is stale, its children poll like explicit AgentProjects.
7. **Cross-repo allow-list takes globs.** `--cross-repo-allowed-projects`
   and `--cross-repo-never-projects` accept the set's glob syntax, with one
   shared matcher. The never-list still wins. The set drops template
   `crossRepo` targets that its own `exclude` matches before it writes a
   child, and names them in its status.
8. **Request counter.** The operator exports
   `ai_operator_gitlab_requests_total{kind, owner}` (kind `rest`,
   `graphql`; owner the AgentProject or AgentProjectSet name) with
   `client_golang`, already in `go.mod`.

## Requirements

### Operator (Go)

- **R1** Request counter (Decision 8), fed from the client's existing
  counts; the poll log line stays. Tests: counts per owner and kind with a
  fake transport.
- **R2** `AgentProjectSet` types, defaults, validation (globs parse,
  `template.gitlab.project` empty), `make generate manifests`, the CRD in
  the chart, a sample in `config/samples/` (Decision 1). Tests: defaults,
  validation errors, CRD schema test.
- **R3** Glob matcher in `internal/pathspec` (or a new package) for `*`,
  `**`, case-insensitive paths; used by the set and the cross-repo flags
  (Decisions 2 and 7). Tests: include and exclude tables, never-list wins,
  existing exact-path flags keep working.
- **R4** GraphQL discovery client in `internal/gitlab`: the query of
  Decision 3, pagination, archived filter, the issue references, complexity
  in the result; transient errors and 429 like the REST client. Tests: a
  fake server with two pages, an error page, a 429 with `Retry-After`.
- **R5** Set reconciler, create and update: discovery each
  `pollInterval`, the template decode (Decision 1), the active rule
  (Decision 4), create and update children (Decisions 2, 5, 7), and the
  status: each matched project as `active`, `dormant`, `excluded`,
  `explicit` or `nameTaken`, counts, `lastDiscoveryTime`; conditions
  `Ready` and `Discovered`. Tests: fake client and fake discovery for
  create, explicit wins, exclude, archived, name taken, template change,
  invalid template, excluded crossRepo target dropped.
- **R5b** Dormant removal (Decision 4): webhook off, wait for the hook to
  go, delete; a child with an AgentTask stays. Tests: removal waits for
  `WebhookRegistered` to clear; a child with a task is kept; a project
  that turns active again gets its child back.
- **R6** Set children poll through the set (Decision 6). Tests: with a
  current discovery, a child poll makes no GitLab request until
  `fallbackInterval`; a changed milestone leads to one full sync; with a
  stale discovery the child polls as before.

### UI server (Go)

- **R7** `GET /api/v1/projectsets` with each set's status from R5, and the
  set name on each child in `GET /api/v1/projects`; the UI service account
  may get and list `agentprojectsets` (`templates/ui-rbac.yaml`).
  `make ts-types`. Tests: handler test with a fake client.

### Web

- **R8** The project list groups children under their set and shows the
  set's counts (active, dormant, excluded, explicit) and last discovery.
  Light and dark, 375px. Tests: view-model unit test; screenshots in the MR.

### Infra (companion)

- **R9** After the release that carries R2–R6: one `AgentProjectSet`
  `jo-wiemers` in `agents` with `include: ["jo.wiemers/**"]`, exclude
  `jo.wiemers/archived/**`, the template from
  `defaults.yaml` plus `webhook.enabled`, a sealed webhook secret, the
  public receiver URL; turn webhooks on for the explicit AgentProjects
  too. The 14 explicit AgentProjects stay (Decision 2). Image bump to that
  release in the same MR.

### Docs

- **R10** `docs/projectsets.md` (what a set does, active and dormant,
  globs, the request budget, migrating an explicit AgentProject), links
  from `docs/usage.md` and the chart README, and this file with a steering
  log.

## Constraints

- Under 300 production lines per MR; tests, generated files and docs do not
  count.
- Without an AgentProjectSet the behaviour stays as before; existing tests
  pass unchanged. Explicit AgentProjects poll exactly as today.
- A set's idle poll is at most `ceil(projects / 100)` GraphQL POSTs; the
  set reports `Ready=False`, reason `TooComplex`, when the query's score
  passes 200 of the limit.
- When a requirement has to be narrowed, open a follow-up issue and say so
  in the steering log; every R has an issue or a steering log line.
- No new dependencies.
- `make test`, `go vet ./...`, `gofmt`, `make helm-lint`, `make ui`,
  `make ts-types`; CI and the drift job green.

## Non-goals

- Group webhooks or system hooks (GitLab Premium).
- Moving the explicit AgentProjects into the set; that is a later infra
  change per project, after their open tasks are done.
- Noticing `/agent` comments in projects without a child.
- Sets across several root groups in one object (write one set per group).
- Per-project overrides inside a set; write an explicit AgentProject.

## Suggested order

R1 → R2 → R3 → R4 → R5 → R5b → R6 → R7 → R8 → R10 → R9 (after the release).

## Acceptance

- With the infra set applied, a new milestone with `/agent spec` in any
  jo.wiemers project that has no explicit AgentProject gets a child
  AgentProject within one set poll and a planned spec task after it.
- An `agent::ready` issue in such a project starts a run the same way.
- An active child with no GitLab activity full-syncs once per
  `webhook.fallbackInterval`, not every `pollInterval`.
- A project in `exclude` or under `archived/` never gets a child, and the
  set's status says why.
- `ai_operator_gitlab_requests_total{kind="graphql"}` grows by at most 2
  per set poll while no child is active.
- A child idle for `dormantAfter` with no task is gone, and so is its
  GitLab hook; a child with an open task stays.
- `/agent repo jo.wiemers/infra/cluster merge` works with
  `--cross-repo-allowed-projects=jo.wiemers/**`.

## Steering log

- 2026-11-16: drafted. The `/agent owner-subjects` line is added only in
  the GitLab milestone description: it carries an e-mail address, and
  this repository is mirrored publicly.
- Status: implemented in this repository; the infra change R9 is still
  open (below). Every R is a merge request below, by its commit subject,
  or a line here.
- R1 (`3e151d6`): `ai_operator_gitlab_requests_total{kind, owner}` as
  specified.
- R3 (`3868624`): the matcher is `internal/projectglob`, shared by the set
  and the cross-repo flags. Existing exact-path values keep working.
- R4 (`e88739c`): the GraphQL client as specified (two pages, an error
  page, a 429 with `Retry-After`).
- R2 (`a41e1ea`): the webhook URL is required too, not only `enabled` and
  `secretRef`: a child that cannot register its hook would never be woken.
  `WebhookRequired` names what is missing.
- R5 was split into two merge requests, the planner (`689e906`, a pure
  function: active rule, explicit wins, exclude, name taken) and the
  reconciler (`98b72cf`: discovery, children, status, conditions).
- R5: a child whose project leaves the set's scope (edited `include` or
  `exclude`, archived) is removed without the `dormantAfter` wait. The
  status gains the count `nameTaken`, which the spec left out of the list.
- R5b (`6ce59db`): a hook that cannot be deleted keeps the child, without a
  timeout; the reason in the status says why. An AgentProject that a human
  deletes still leaves its GitLab hook, as before.
- R6 (`c7a162f`): as specified. A child skips the light poll while the
  set's discovery is younger than twice its `pollInterval`.
- R7 (`d55d03d`): `GET /api/v1/projectsets` and the set name on each
  project, as specified.
- R8 (`61aa49d`): the Desk's set line leaves out zero counts except
  `active`.
- R9: stays in the infra repository, after the release that carries R2 to
  R6. The 14 explicit AgentProjects stay (Decision 2). The infra
  cross-repo allow-list is kept explicit: it lists the companions by full
  path and does not use the new globs, so a companion is a decision per
  project and the set's reach does not widen it.
- R9 (#560): not applied yet. The plan gave #560 no companion repository,
  so its merge request carries only this line and the infra change is
  still to do in `jo.wiemers/infra/cluster`, by hand or by a follow-up
  issue labelled `agent-repo::jo.wiemers/infra/cluster`. The change: the
  set `jo-wiemers` in `agents` (group `jo.wiemers`, token `gitlab/token`,
  include `jo.wiemers/**`, exclude `jo.wiemers/archived/**`, template =
  `defaults.yaml` without `gitlab`, plus `webhook.enabled`,
  `webhook.secretRef` `gitlab-webhook`/`token` and `webhook.url`); the same
  `webhook` block once in `defaults.yaml` for the 14 explicit
  AgentProjects; the public route to `POST /hooks/gitlab` if infra has
  none; the image bump to the release after R6. Receiver URL and the sealed
  secret are not decided yet: seal it with the cluster's certificate
  (`kubectl -n agents create secret generic gitlab-webhook
  --from-literal=token=… --dry-run=client -o yaml | kubeseal -o yaml`),
  never commit a plain Secret. The acceptance checks after rollout
  (`Ready` and `Discovered`, `explicit` entries, `WebhookRegistered`, the
  GraphQL counter) are recorded here when R9 lands.
- R10 (#559): `docs/usage.md` and `docs/projectsets.md` predate the docs
  layout. The guide is `docs/guides/project-sets.md`, the fields are
  `docs/reference/agentprojectset.md`, and the links come from the
  guides, reference and getting-started pages, the glossary, the design
  page and the chart README.

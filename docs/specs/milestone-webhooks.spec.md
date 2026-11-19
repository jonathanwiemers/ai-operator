# Webhooks tell of milestones too, so idle projects stop polling GitLab

Status: implemented in one merge request by hand, not run as an autopilot
milestone, to cut GitLab requests quickly. Deviations from the requirements
go into the [Steering log](#steering-log).

## Goal

1. A project with a working webhook makes no GitLab REST requests between its fallback full syncs, busy or idle. Milestone changes reach it through a milestone webhook or through its set's group-wide GraphQL poll, which costs the same for 1 project or 100.
2. An idle project with a healthy hook stays `WebhookDeliveries=True`. It does not fall back to a full sync every `pollInterval` just because nothing happened.
3. A dormant project in an AgentProjectSet tells the set within seconds when it gets a spec milestone or an `agent::ready` issue, and its child is created right away.
4. Metrics show how many GitLab requests are polling and how many are work, per project and per client call.

## Background

File refs are at origin/main 144008d7, 2026-11-19. Live on netcup01 since infra/cluster !325 (operator 2026.11.285): webhooks on bloop and ai-operator, `fallbackInterval: 5m`, AgentProjectSet `jo-wiemers` over the whole group, bloop and ai-operator kept as explicit AgentProjects.

Open autopilot milestones run first, in order: %18–%38, of which %31, %32, %34, %36, %37 and %38 still have pending work. This milestone builds on %29 (AgentProjectSet, merged) and complements %31 (settling milestones make fewer calls). Neither of them changes the hook options or the receiver's routing.

- **Hook events.** Hooks register with issues, notes, merge requests, pipelines and pushes (`internal/controller/webhook.go:65`), not milestones. The hook API takes `milestone_events` (GitLab 18.2+). Milestone hooks fire on `create`, `close`, `reopen` and `delete`, with the full `object_attributes.description`. They **never fire on update**: a spec header or text edited into an existing milestone sends nothing.
- **Light milestone poll.** While deliveries arrive, an AgentProject still calls `milestonesChanged` (`internal/controller/agentproject_controller.go:268`), a REST milestone list, every `pollInterval` (2m). Set children skip it while their set's discovery is current (`setDiscoveryCurrent`, `agentproject_controller.go:249`). Explicit AgentProjects cannot skip it: `setDiscoveryCurrent` only follows a controller ownerRef.
- **Set discovery.** It already reads `milestones(state: active, first: 20) { iid title description updatedAt }` for every project (`internal/gitlab/discovery.go:221`), 2 GraphQL requests for 112 projects. `milestoneChanges` (`internal/controller/projectset_wake.go`) hashes iid, title and `updatedAt`, so description edits change the hash. It only wakes entries with a set-owned `Child`, not explicit ones.
- **Silence.** A project counts as receiving only within `webhook.silenceThreshold` (default 30m) of its last delivery (`webhook.go:140`). On 2026-11-19, bloop's hook 93737175 was healthy (`alert_status: executable`), but nothing had happened in bloop since it was made. bloop stayed `Silent` with a full sync every 2m, at about 7.5 REST requests/min.
- **GitLab API.** `POST /projects/:id/hooks/:hook_id/test/:trigger` sends a test delivery (Maintainer, 5/min per project and user). `GET /projects/:id/hooks/:hook_id/events` lists a hook's last 7 days of deliveries (17.3+). The bot `operator-ai` is group Maintainer.
- **Dormant projects** have no AgentProject and so no hook. A new spec milestone or ready issue there is seen only on the set's next discovery (2m).
- **Metrics.** `ai_operator_gitlab_requests_total{kind, owner}` (`internal/controller/metrics.go:25`) cannot tell polling from work. On 2026-11-19, ai-operator made about 29 REST requests/min while `Receiving`, mostly for an active autopilot run.

## Decisions (locked)

1. **Milestone events on every hook.** Child and explicit hooks add `milestone_events: true`. The new options change the config hash, so existing hooks are updated in place on their next check. The receiver needs no special case: like every event, a `Milestone Hook` delivery records a delivery and wakes the project. Rejected: parsing the milestone payload to skip the sync. The sync reads the milestones anyway.
2. **Edits come from the set's GraphQL poll, not REST.** GitLab has no milestone update hook. The set's existing `updatedAt` hash watches edits for every project it discovers, explicit ones included. A project its set covers never runs `milestonesChanged`. A project no set covers keeps today's light poll. Manual nudge for an urgent edit: close and reopen the milestone, which fires two hooks. Rejected:
   - polling `GET .../hooks/:id/events`: it shows deliveries, not edits;
   - a per-project GraphQL `updatedAt` poll: it costs per project, the set's poll does not.
3. **Explicit projects count as covered.** `setDiscoveryCurrent` also accepts an AgentProject without a set ownerRef when a set in the same namespace lists its GitLab path as `explicit` and the set's discovery is current (within 2× its pollInterval). `milestoneChanges` stores the hash for `explicit` entries too. When the hash changes, it records a delivery and wakes the explicit AgentProject. Rejected: making explicit projects set children. A set template is uniform, and deleting an AgentProject deletes its tasks.
4. **A heartbeat proves an idle hook works.**
   - When a project's last delivery is older than `silenceThreshold / 2`, its sync fires `POST .../hooks/:id/test/push_events`, at most once per `silenceThreshold / 2`, jittered by project.
   - On a 4xx (for example an empty repository) it tries `issues_events`, then `milestone_events`, and keeps the first trigger that works.
   - The test delivery arrives like any other and keeps the project `Receiving`. If none arrives, the project falls back to `Silent` and a full sync every pollInterval, as today.
   - The delivery store marks heartbeat deliveries, so the sync they wake is not a full sync.
   - Cost: 1 request per project per 15m with the default threshold.

   Rejected: treating `alert_status: executable` as proof. It does not show that the route, gateway and token work end to end.
5. **The set owns hooks on dormant projects.**
   - For each `dormant` entry without a child, the set registers one project hook with only `issues_events` and `milestone_events`, using the template's `webhook.url` and `webhook.secretRef`. Its id and check time live in the set's status entry. It is rechecked hourly, like child hooks.
   - The set deletes that hook when the entry turns active and the child reports `WebhookRegistered=True`, when the entry becomes excluded or archived, leaves the include, or disappears from discovery.
   - A finalizer on the set deletes its remaining hooks. After 3 failed attempts per hook it logs and lets go.

   Rejected: creating a child for every project. Children cost a full sync each, and a dormant child is removed after `dormantAfter`.
6. **The receiver wakes the set for set hooks.** When no AgentProject matches a delivery's path, the receiver looks for a set whose status lists that path with a set hook. It checks the token against that set's template secret. On a match it wakes the set, at most once per set per 30s, and the woken reconcile runs discovery at once. Unknown paths and wrong tokens still get the same 401 as today.
7. **The set keeps its 2m poll.** It is the only edit watcher and costs about 1 GraphQL request per 100 projects per poll. No new interval field.
8. **Metrics tell polling from work.**
   - `ai_operator_gitlab_requests_total` gets an `op` label: the GitLab client method name, a fixed set.
   - New: `ai_operator_webhook_deliveries_total{event, result}`, with result `accepted`, `heartbeat`, `set` or `refused`.
   - New: `ai_operator_project_syncs_total{namespace, project, kind}`, with kind `full`, `light` or `skipped`.

## Requirements

### Operator (Go)

- **R1 Milestone events on hooks.** `gitlab.ProjectHookOptions` and `gitlab.ProjectHook` carry `MilestoneEvents`. `webhook.go` sets it and compares it in the hook-match check, so existing hooks get it on their next recheck (Decision 1). Tests: a hook without milestone events is updated in place, keeping its id; a matching hook is left alone.
- **R2 Metrics.** Add the `op` label and the two counters (Decision 8). Every GitLab client call passes its method name, and every sync counts its kind. Tests: one client call increments its `op` series; a skipped light poll counts as `skipped`.
- **R3 Explicit projects covered by the set.** `setDiscoveryCurrent` and `milestoneChanges` as in Decision 3. While covered, the `WebhookDeliveries` message says `milestones watched by set <name>`. Tests:
  - an explicit project with a current set skips `milestonesChanged`;
  - with a stale set (older than 2× pollInterval) it polls;
  - an edit (`updatedAt` changes) wakes it;
  - a project the set does not list as explicit is not covered.
- **R4 Truncation guard.** Discovery marks a project whose active milestones fill the page (20) as truncated. A truncated project is not covered and keeps its own light poll, and the set entry shows it. Tests: 20 milestones make the project truncated and uncovered; 19 do not.
- **R5 Heartbeat.** As in Decision 4. The trigger in use is kept in `status.webhook.heartbeatTrigger` and the last one in `status.webhook.heartbeatTime`. A heartbeat delivery does not force a full sync. Tests:
  - a heartbeat is due after half the silence threshold, and never twice within it;
  - a 4xx falls back to the next trigger and the working one is stored;
  - a heartbeat delivery wakes a light sync, not a full one;
  - no delivery within the threshold still turns `Silent`.
- **R6 Set hooks on dormant projects.** As in Decision 5. The status entry gets `hookID` and `hookCheckedAt`. Tests:
  - a dormant entry gets a hook with only issues and milestone events;
  - the hook is deleted when the child registers its own;
  - it is deleted when the entry becomes excluded or archived, or disappears;
  - the finalizer deletes the remaining hooks and lets go after 3 failures.
- **R7 Receiver routes to sets.** As in Decision 6. Add a watch source so the receiver can wake a set, with the 30s debounce. Tests:
  - a delivery for a set-hook path with the right token wakes the set once, even if two arrive within 30s;
  - a wrong token gets 401;
  - an unknown path gets 401;
  - a path with both an AgentProject and a set hook wakes the AgentProject.

### Docs

- **R8** Update `docs/reference/agentproject.md` (milestone events, the heartbeat, the `heartbeatTrigger` and `heartbeatTime` status, the new `WebhookDeliveries` message) and `docs/reference/agentprojectset.md` (explicit projects covered, set hooks on dormant projects, the truncation flag). In `docs/contributing/`, describe how deliveries, the heartbeat and the set's edit watch fit together, with the metrics. Add the spec file `docs/specs/milestone-webhooks.spec.md` with a steering log, and a line in `docs/specs/README.md`. Tests: `go test ./internal/docscheck/`.

## Constraints

- One merge request (owner's decision), over the usual production-line budget; tests, generated files and docs do not count.
- GitLab Free: the bot has 100 requests/min. Heartbeats and set hook checks are jittered by project and never burst after a restart. The first heartbeat comes no earlier than `silenceThreshold / 2` after start.
- Stays the same:
  - every AgentProject or set spec field and its default;
  - the 401 answer for unknown projects and wrong tokens;
  - projects with `webhook.enabled: false`, which poll as today;
  - the light poll of projects no set covers.
- New CRD status fields only as named above. Run `make generate manifests` after changing the types.
- No new dependencies.
- Checks: `gofmt`, `go vet ./...`, `make helm-lint`, and the tests of the changed packages. CI runs `make test`.
- If a requirement has to be narrowed, open a follow-up issue and log it in the spec's steering log.

## Non-goals

- Group webhooks (GitLab Premium only).
- A webhook for milestone edits (GitLab has none), or lowering the set's 2m poll.
- Scraping the operator's metrics in the cluster. The owner adds the pod annotations in infra values afterwards.
- The tempting next step: driving the Desk's live views from deliveries instead of its own refresh.

## Suggested order

R1, R2 → R3, R4 → R5 → R6 → R7 → R8

## Acceptance

- On netcup01 with bloop and ai-operator explicit in set `jo-wiemers`, both show `milestones watched by set jo-wiemers`. `ai_operator_project_syncs_total{kind="light"}` stays flat for both, and `ai_operator_gitlab_requests_total{op="ListMilestones"}` stops growing for them (Goal 1).
- Editing a bloop milestone's description starts bloop's sync within 2× the set's pollInterval (Goal 1).
- Creating a `/agent spec` milestone in bloop starts its sync within 10s (Goal 1).
- An idle bloop stays `WebhookDeliveries=True` for 24h through heartbeats. Its REST rate while idle is under 1/min, down from about 7.5 (Goal 2).
- Adding an `agent::ready` issue or a spec milestone to a dormant project of the set (e.g. agents/skills) creates its child within 30s. The set's hook there is gone once the child's own hook registers (Goal 3).
- `ai_operator_webhook_deliveries_total` and the `op` label show accepted, heartbeat and set deliveries and per-call request counts on the operator's `/metrics` (Goal 4).

## Requirements and where they were implemented

| Requirement | Implemented in |
| --- | --- |
| R1 milestone events on hooks | `internal/gitlab/client.go`, `internal/controller/webhook.go` |
| R2 metrics | `internal/gitlab` request counting, `internal/controller/metrics.go`, `internal/webhook/receiver.go` |
| R3 explicit projects covered | `internal/controller/agentproject_controller.go` (`setDiscoveryCurrent`), `projectset_wake.go` |
| R4 truncation guard | `internal/gitlab/discovery.go`, `api/v1alpha1` `milestonesTruncated`, `projectset_wake.go` |
| R5 heartbeat | `internal/controller/heartbeat.go`, `internal/webhook/deliveries.go`, `api/v1alpha1` `heartbeatTrigger`/`heartbeatTime` |
| R6 set hooks on dormant projects | `internal/controller/projectset_hooks.go`, `agentprojectset_controller.go`, `api/v1alpha1` `hookID`/`hookCheckedAt`, RBAC `agentprojectsets/finalizers` |
| R7 receiver routes to sets | `internal/webhook/receiver.go` (`wakeSets`), `AgentProjectSetReconciler.WakeSet`, `cmd/operator/main.go` |
| R8 docs and this spec | `docs/reference/agentproject.md`, `agentprojectset.md`, `docs/guides/project-sets.md`, `docs/contributing/design.md`, this file |

## Steering log

- 2026-11-19: the owner asked for one merge request instead of an autopilot
  milestone, so the requests drop sooner. No GitLab milestone was created.
- R5: a project its set covers gets no heartbeat: the set's discovery keeps
  it receiving already. The heartbeat time is stamped even when every
  trigger fails, so a broken hook costs one try per half threshold.
- R6, handover: the child's own `syncWebhook` finds the set's hook by URL
  and adopts it (edits it to all events and its own token). When an
  AgentProject's `status.webhook.hookID` equals the entry's `hookID`, the
  set forgets the hook instead of deleting it.
- R6, budget: at most 6 hook requests per set reconcile (3 new hooks per
  poll), each hook rechecked hourly plus up to 15 minutes of jitter by path;
  `hookCheckedAt` is stamped on a failure too, which backs off.
- R6, a just-created child (in the entry but not yet listed) keeps the
  set's hook until it registers its own.
- R6, a vanished path whose hook delete fails stays an `excluded` entry
  until the delete works; the failure counter lives in memory.
- R6, **narrowing:** a new token in the template's `webhook.secretRef` is
  not pushed to existing set hooks (their config is not hashed). They get
  it when created anew; deliveries with the old token are refused and the
  set's 2m poll covers the gap.
- R7: the set's waker uses a 30 s window; a woken reconcile skips the idle
  early return and discovers at once.

# Sandbox test plan

The unit and end-to-end tests use a fake GitLab, scripted agents and local git
repositories. This plan runs the whole system for real: a kind cluster, a
GitLab project, and the real `claude` / `cursor-agent` CLIs.

Plan for about half a day. With the sandbox limits, model usage is a few
dollars at API prices, or a modest share of a subscription window.

## What this test is most likely to find

These parts are not covered by automated tests, so look here first:

| Area | Risk |
|------|------|
| `claude -p --output-format json` | field names (`session_id`, `total_cost_usd`, `structured_output`), behaviour of `--json-schema`, `--resume` and `--effort` |
| `cursor-agent -p` | flags (`--force`, `--model auto`, `--resume`), output format, JSON answers folded into the prompt |
| Volumes | sub-path creation by the init container, `fsGroup`, permissions of `/workspace` and `/runner` |
| Service account | the explicitly projected token works with client-go in the runner |
| Agent image | both CLIs installed, `HOME` on the PVC, Cursor CLI outside `$HOME` |
| GitLab | real API behaviour (scoped labels, pagination, MR creation, note authors) |
| Prompts | quality of plans, sub-specs, profile choice, review findings |
| Usage limits | the exact error texts the CLIs print when a subscription window or rate limit is hit |
| CI loop | pipeline lookup by commit for branch and MR pipelines, job log format of your runners |

## 0. Prerequisites

* Docker, [kind](https://kind.sigs.k8s.io/), `kubectl`, `helm`, a clone of this
  repository.
* A GitLab project for the sandbox, e.g. `<you>/sandbox-todo` on gitlab.com.
* A **project access token** for it: role *Developer*, scopes `api` and
  `write_repository`. The bot user behind it is what the agent's comments are
  attributed to.
* `CLAUDE_CODE_OAUTH_TOKEN` (run `claude setup-token`) or `ANTHROPIC_API_KEY`.
* Optional: `CURSOR_API_KEY`. With it, the `fast` profile and the reviewers
  use `cursor` with model `auto`; without it they use `claude-sonnet-5-5`.

```sh
export GITLAB_URL=https://gitlab.com            # or your instance
export GITLAB_PROJECT=<you>/sandbox-todo
export GITLAB_TOKEN=glpat-...
export CLAUDE_CODE_OAUTH_TOKEN=...              # or ANTHROPIC_API_KEY=...
export CURSOR_API_KEY=...                       # optional
```

## 1. Setup

### 1.1 GitLab project

```sh
# Push the sample app (a tiny dependency-free Node todo API) to the project
cp -r hack/sandbox/sample-repo /tmp/sandbox-todo && cd /tmp/sandbox-todo
git init -b main && git add . && git commit -m "Initial sample app"
git push "https://oauth2:${GITLAB_TOKEN}@${GITLAB_URL#https://}/${GITLAB_PROJECT}.git" main
cd -

# Create the workflow labels
hack/sandbox/gitlab-setup.sh
```

In GitLab: protect `main` (it usually is already), and create an issue board
with the lists `agent::draft`, `agent::ready`, `agent::working`,
`agent::needs-input`, `agent::review`, `agent::done`.

### 1.2 Cluster and operator

```sh
hack/sandbox/up.sh
```

This builds both images locally, creates the kind cluster `ai-operator`,
installs the chart from `charts/ai-operator` with debug logging, creates the
secrets in namespace `agents`, and applies `hack/sandbox/agentproject.yaml`.
The sandbox limits are: `maxConcurrentRuns: 1`, `idleTimeout` at its default
(`1m`), `retentionPeriod: 1h`, `maxRunsPerTask: 6`, small `maxBudgetUSD`,
`review.maxIterations: 1`.

Keep two terminals open:

```sh
kubectl -n agents get agentprojects,agenttasks,pods -w
kubectl -n ai-operator-system logs -f deploy/ai-operator
```

## 2. Test cases

Record each result in the table in section 4. "Pod" means the task's pod,
which has the task's name, e.g. `sandbox-spec-m1-tags-and-due-dates` or
`sandbox-2-add-tags-to-todos`.

### T1 – Operator connects to GitLab

1. `kubectl -n agents get agentproject sandbox -o yaml`

**Expected:** condition `Synced=True`, `status.botUserID` set,
`status.lastSyncTime` updated every 30s. The operator log shows no errors.

### T2 – Planning a milestone

1. Create a milestone **Tags and due dates** and paste
   [`hack/sandbox/milestone-spec.md`](../hack/sandbox/milestone-spec.md) as its
   description. The description contains the line `/agent spec`.
2. Wait for the next poll.

**Expected:**
* A task `sandbox-spec-m<iid>-tags-and-due-dates` (type `Spec`) and a pod with
  the same name. It has an init container `init-workspace`, then containers
  `runner` and `agent`, both `Running`.
* `kubectl -n agents logs <pod> -c runner` shows "starting run", a `step
  started` / `step done` pair for each of the 9 spec steps (read milestone …
  link dependencies, one "issue created" line per issue) and "run finished" with
  the step list. While it runs, `kubectl -n agents get agenttasks` shows the
  step in the `Step` column.
* A few (likely 3) new issues in the milestone with the labels
  `ai-generated`, `agent::draft`, `agent-profile::fast|strong`. Each holds a
  self-contained sub-spec (context, plan with real file names such as
  `src/server.js`, acceptance criteria, test plan), `Depends on #…` where it
  makes sense, and a question about tag normalisation with a suggested
  default. Where the planner made choices, the bot commented on the issue
  with **Decisions of the plan** (question, choice, why, alternatives) with
  IDs `D<iid>-P1`, `D<iid>-P2`, …; the description only points to it.
* Issues with `Depends on #…` are linked to those issues: **relates to** on
  GitLab Free, **is blocked by** on Premium (the run log's `link
  dependencies` step says which).
* The milestone description is unchanged.
* `kubectl -n agents get agenttask <spec task> -o jsonpath='{.status.run}'`
  shows `lastResult: Succeeded`, a summary and a cost.

Judge the plan quality as well: is the split sensible, and are the profile
choices plausible?

### T3 – Re-planning

1. Change the profile label of one draft issue by hand, e.g. `fast` → `strong`.
2. Edit the milestone description: add the requirement "`GET /todos?tag=` accepts
   several comma-separated tags (todo must have all)".

**Expected:** the spec task's `runRequest` goes up by 1 and one new run
happens. Draft issues are updated (no duplicates), and the hand-changed
profile label is kept. Editing the description again *without* a change
(e.g. only whitespace at the end) does not trigger a run.

### T4 – Implementing a `fast` issue

1. Pick a draft issue with `agent-profile::fast`. Answer its questions in a
   comment, e.g. "Tags: lowercase, trimmed, deduplicated."
2. Move it to `agent::ready`.

**Expected:**
* Within one poll, the label becomes `agent::working` and a task and pod
  `sandbox-<iid>-<slug>` appear.
* The runner log shows the profile `fast` with its engine and model on the
  `run implementer` step.
* The reviewers run (two more `agent CLI started` lines in the agent log,
  with the same `run` value as the runner's lines).
* The issue gets a **📌 Decisions** comment with IDs `D<iid>-1`, … (question,
  choice, why, alternatives), and the runner log has a `post decisions` step.
* The issue gets a **🧠 Agent notes** comment with the implementer's handoff
  notes; later runs edit it instead of adding a new one.
* Right at the start, a **draft MR** "Draft: <title>" with `Closes #<iid>` is
  opened on branch `agent/<iid>-<slug>`, whose first commit "Start work on
  #<iid> [skip ci]" starts no pipeline. The issue gets a comment linking it.
* While the run goes on, the MR has a **progress comment** whose run log grows
  step by step (refresh the page); when the run ends it is replaced by the
  report, which ends with a collapsed **Run log** table (read issue …
  update agent notes) with durations and the cost.
* The branch is pushed. The issue stays in `agent::working` until the MR pipeline is
  green – with the sample app's `changelog` job that takes one CI fix round
  (see T17) – and then becomes `agent::review`.
* The `test` job passes on the branch, and new tests exist.
* The task's `status.run` shows `lastResult: Succeeded`, `branch`,
  `mergeRequestURL` and the cost.

### T5 – Implementing a `strong` issue, questions, resume

1. Move a `strong` draft issue to `agent::ready` **without** answering its
   questions.

**Expected:** either the agent decides with its stated defaults and opens an
MR, or it stops with `agent::needs-input` and posts its questions as a
comment. Both are acceptable; note which.

2. Answer the questions in a comment that does **not** start with `/agent`.

**Expected:** nothing happens (no new run, label unchanged).

3. Comment `/agent please continue with these answers`.

**Expected:** a new run within one poll (`runRequest` +1, label
`agent::working`). It **resumes** the previous session: there are no
"could not resume session" lines in the runner log, and the cost of this
run is noticeably lower than the first one. The answers are reflected in
the code.

### T6 – Iterating on the MR

1. Leave a review comment on the MR, e.g. "Return 422 instead of 400 for
   invalid dates", and a second comment `/agent apply the review comments`.

**Expected:** one new run. A new commit is pushed to the same branch, a
comment with the summary is posted on the MR, and the label goes back to
`agent::review`.

2. Push a commit to the agent's branch yourself (e.g. edit `README.md`), then
   comment `/agent rebase your work on my change and add a CHANGELOG entry`.

**Expected:** the run builds on your commit (your commit is in the branch
history below the agent's new commit), and the push is not forced.

3. Comment `/agent revisit D<iid>-1 <a different choice>` with one of the
   IDs from the decision comment, and right after it, in a second comment,
   `/agent revisit D<iid>-99 anything`.

**Expected:** a run (two, if a poll falls between the comments). The code
follows the new choice, a new decision comment has a decision with
*Supersedes D<iid>-1*, the old decision shows *Superseded by …*, and an MR
comment says that D<iid>-99 was not found. Revisiting D<iid>-1 again is not
applied: the agent is told to ignore it, and the report says it is
superseded.

### T7 – No triggers from the bot or from chatter

**Expected (check throughout):** the agent's own comments never trigger a run;
comments like "/agents are cool" or "looks good" do not trigger a run.

### T8 – Queueing

1. Move two `draft` issues to `agent::ready` at the same time.

**Expected:** with `maxConcurrentRuns: 1`, one task is `Running` and the
other `Queued` ("waiting for capacity"). It starts once the first run
finishes. The queued issue already shows `agent::working`.

### T9 – Lifecycle: idle, hibernate, archive, wake up

Use an issue from T4 that is in `agent::review`.

1. Right after its run, the task is `Idle` and the pod still runs.
2. About a minute later (`idleTimeout`): the task is `Hibernated`, the pod
   is gone, and the PVC `<task>-ws` still exists. While the MR pipeline runs
   the task stays hibernated; a CI fix run starts a new pod.
3. Comment `/agent add a test for an empty tag list`.

**Expected:** the pod comes back, the run resumes the session (the PVC kept
`$HOME`), and the push and MR comment work.

4. After 1 hour without activity (`retentionPeriod`): the task is `Archived`
   and the PVC is gone.
5. Comment `/agent …` again.

**Expected:** a new PVC and pod. The runner state and the agent session were
on the deleted PVC, so the run starts a **fresh session** with the full issue
thread and the **🧠 Agent notes** comment (the runner log's `read comments`
step says "agent notes from an earlier run"), clones the repository again, and continues on the existing branch
and MR (the branch name is kept in the task's status).

### T10 – Finishing an issue

1. Merge an MR (it closes the issue) – or move another issue to `agent::done`.

**Expected:** within one poll, the task, its pod and its PVC are deleted.

### T11 – Run limit

1. `kubectl -n agents patch agentproject sandbox --type merge -p '{"spec":{"lifecycle":{"maxRunsPerTask":1}}}'`
2. Comment `/agent …` on an issue whose task already ran once.

**Expected:** the task becomes `Blocked` with the message "reached
maxRunsPerTask (1)", and its pod is stopped. Set the limit back to `6`; the
pending run starts right away.

### T12 – Failure handling

1. Set the `strong` profile's model to `does-not-exist`
   (`kubectl -n agents edit agentproject sandbox`; a merge patch would
   replace the whole profiles list) and move a `strong` issue to
   `agent::ready`.

**Expected:** the run fails cleanly. The issue gets a comment "The agent run
failed" with the error and the label `agent::needs-input`; the task shows
`lastResult: Failed`. Revert the model, comment `/agent retry`, and the run
succeeds.

### T13 – Security: credential isolation

On any running pod (pods stop a minute after their run: check while a run is
in progress or right after it):

```sh
POD=<pod>
# The agent container has no GitLab token, no service account token, no runner state
kubectl -n agents exec $POD -c agent -- sh -c 'env | cut -d= -f1 | sort'
kubectl -n agents exec $POD -c agent -- ls /var/run/secrets/kubernetes.io/serviceaccount   # must fail
kubectl -n agents exec $POD -c agent -- ls /runner                                        # must fail
# The runner has them
kubectl -n agents exec $POD -c runner -- ls /var/run/secrets/kubernetes.io/serviceaccount
kubectl -n agents exec $POD -c runner -- ls /runner                                       # mirror.git, state.json
```

**Expected:** `GITLAB_TOKEN`, `KUBERNETES_*` and `AI_TASK_*` are absent from
the agent container's environment. Its engine credentials are present.

Then run the two probe issues from
[`hack/sandbox/probe-issues.md`](../hack/sandbox/probe-issues.md). They ask the
agent to report its environment, and to plant a malicious `core.fsmonitor`
and git hook. Check the results as described there. If the agent refuses
probe 2, run the same `git config` command yourself with
`kubectl exec … -c agent` inside `/workspace/repo`, then trigger a run with
`/agent`.

### T14 – Network policy (optional)

Needs a CNI that enforces NetworkPolicies. kind's default CNI may not; use a
kind cluster with Calico or Cilium if you want to test this.

```sh
API=$(kubectl get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}')
helm upgrade ai-operator charts/ai-operator -n ai-operator-system --reuse-values \
  --set agent.networkPolicy.enabled=true --set "agent.networkPolicy.allowedCIDRs={${API}/32}"
```

**Expected:** runs still work (GitLab, model APIs, npm reachable). Probe 1
step 5 (metadata endpoint) times out. Without `allowedCIDRs` the runner fails
to reach the Kubernetes API, which confirms the policy is enforced.

### T15 – Helm upgrade and uninstall

1. Rebuild with a new tag (`TAG=sandbox2 hack/sandbox/up.sh`).

**Expected:** the operator rolls, the CRDs are updated, and existing tasks
continue. Agent pods only pick up the new image when they are recreated
(after hibernation, or after deleting the pod).

2. `helm uninstall ai-operator -n ai-operator-system`

**Expected:** the CRDs and the AgentProject still exist (`helm.sh/resource-policy: keep`).

### T16 – Usage limits

A real limit is hard to provoke on purpose; if you hit one during the other
tests, check the expectations below on that issue. Otherwise simulate one:
while an issue is in `agent::review`, mark its task as limited and request a
run.

```sh
UNTIL=$(date -u -d '+10 min' +%Y-%m-%dT%H:%M:%SZ)   # macOS: date -u -v+10M +%Y-%m-%dT%H:%M:%SZ
kubectl -n agents patch agenttask <task> --subresource=status --type merge \
  -p "{\"status\":{\"run\":{\"deferredUntil\":\"$UNTIL\",\"limitedEngine\":\"claude\",\"deferrals\":1}}}"
```

Then comment `/agent add a code comment to the store`.

**Expected:**
* The task is `Waiting` with "claude usage limit; retrying at …", no run
  starts, and `kubectl get agenttasks -o wide` shows the retry time.
* Within a poll, the project status lists the limit
  (`kubectl -n agents get agentproject sandbox -o jsonpath='{.status.usageLimits}'`).
* Another issue whose profile uses claude, moved to `agent::ready` now, is
  deferred immediately (its runner log says "usage limit reached, deferring
  run" without calling the CLI), and gets the "⏳" comment.
* After the retry time both runs happen normally and the deferral fields are
  cleared.

### T17 – CI feedback loop

The sample app ships a `.gitlab-ci.yml` with a `changelog` job the agent is
not told about: every MR must add a line to `CHANGELOG.md`. The first push of
an agent MR therefore fails it. This needs CI runners for the project (on
gitlab.com, shared runners with a verified account).

1. Watch any issue from T4/T5 after the agent's first push.

**Expected:**
* The issue stays in `agent::working` while the pipeline runs;
  `kubectl -n agents get agenttasks` shows the CI state (`Running`).
* The pipeline fails on `changelog`; the failed jobs are retried once (a
  retry appears in the pipeline), then the task goes to `Fixing` and a new
  run starts. The runner log shows the CI fix run; the MR gets a "CI fix: …"
  comment and a commit that adds a `CHANGELOG.md` entry.
* The next pipeline is green, the CI state `Success`, and the issue moves to
  `agent::review`.

2. Make the failure unfixable: on the default branch, change the `changelog`
   job's script to `exit 1`, then trigger a run on an issue (`/agent add a
   comment to the store`).

**Expected:** the same job now fails on the default branch too, so the
operator does not involve the agent: the issue goes to `agent::review` with a
comment that the failure also happens on the default branch. Revert the
change afterwards.

3. Optional: set `spec.ci.maxFixRounds: 0` and repeat step 1 on another issue.

**Expected:** after the retry the issue goes straight to `agent::needs-input`
with the failing job named.

## 3. When something fails

Collect this before changing anything:

```sh
kubectl -n agents get agenttask <task> -o yaml
kubectl -n agents describe pod <pod>
kubectl -n agents logs <pod> -c init-workspace
kubectl -n agents logs <pod> -c runner
kubectl -n agents logs <pod> -c agent
kubectl -n ai-operator-system logs deploy/ai-operator --since=30m
kubectl -n agents get events --sort-by=.lastTimestamp | tail -30
```

For CLI problems, reproduce inside the agent container, where the same
environment and credentials are available:

```sh
kubectl -n agents exec -it <pod> -c agent -- sh -c 'cd /workspace/repo && claude -p "say hi" --output-format json'
kubectl -n agents exec -it <pod> -c agent -- sh -c 'cd /workspace/repo && cursor-agent -p "say hi" --output-format json --model auto'
```

File an issue in this repository with the test case ID, what happened, and
the collected output (remove tokens first).

### T18 – Autopilot milestone

Preparation: allow the bot to merge into `main` (Protected branches,
"Allowed to merge: Developers + Maintainers").

1. Create a milestone **Autopilot demo** with `/agent autopilot` and a small
   two-part spec, e.g. "Add `GET /api/health` returning `{ok: true}`, then
   document it in README.md" (the second part depends on the first).

**Expected:**
* The planned issues have no open questions; each has a bot comment
  **Decisions of the plan** with the defaults it took and why. A tracking issue **🤖 Autopilot: Autopilot demo**
  appears with one line per issue.
* Only the first issue moves to `agent::ready`, then `agent::working`. Its
  run posts a decision comment, the draft MR is opened, the pipeline runs.
* Once the pipeline is green, the MR is marked ready and merged by the bot;
  the issue is closed, the tracking issue shows it as merged with its
  commit and decisions.
* Then the second issue starts on the new `main` and is merged the same way.
  The tracking issue ends with "Autopilot is done" and is closed.

2. Repeat with a new milestone and, while the first MR's pipeline runs, push a
   commit to `main` that touches another file.

**Expected:** if the project merges fast-forward or semi-linear, the MR is
rebased once by GitLab, CI runs again, and the MR is merged. With merge
commits nothing special happens.

3. Make the pipeline fail for good (the `changelog` job, see T17) and let the
   fix rounds run out.

**Expected:** the issue lands in `agent::needs-input` and the tracking issue
says *Autopilot paused* with the reason. Fix the job by hand, move the issue
to review and comment `/agent resume` on the tracking issue: the MR is merged
and the queue continues. `/agent pause` and `/agent skip #<iid>` work as
described in usage.md.

4. On the tracking issue, comment `/agent revisit D<iid>-1 <a different
   choice>` with an ID from the decision index of a merged issue.

**Expected:** the bot answers *Created #N to revisit … It is queued on
autopilot.* The follow-up **Revisit D…: …** is a draft in the milestone and is
worked on and merged after the planned issues. When its decisions are posted,
the old decision on the merged issue says *Superseded by …*. A second revisit
of the same ID is answered with a pointer to the follow-up.

5. Remove `/agent autopilot` from the milestone.

**Expected:** the tracking issue gets *Autopilot is off* and is closed;
nothing is merged any more.

### T19 – Merge conflicts

1. Let the agent implement an issue that changes a line of `README.md` and
   wait until it is in `agent::review`.
2. Change the same line on `main` by hand.

**Expected:** within a poll the issue goes back to `agent::working`. The run
log shows `merge main` with the conflicting file, `run implementer` and
`conclude merge`. The branch gets a merge commit with both changes and no
conflict markers, the MR comment says *Merged `main` into the branch and
resolved the conflicts in `README.md`*, CI runs and the issue returns to
`agent::review`.

3. Repeat, but make the conflict impossible to resolve sensibly (e.g. delete
   the file on `main`) and tell the agent in an `/agent` comment to ask
   instead of guessing.

**Expected:** nothing is pushed, the issue goes to `agent::needs-input`, and
the MR comment names the files. On autopilot the tracking issue says it
paused.

## 4. Results

| ID | Test | Result | Notes |
|----|------|--------|-------|
| T1 | Operator connects to GitLab | | |
| T2 | Planning a milestone | | |
| T3 | Re-planning | | |
| T4 | Implementing a `fast` issue | | |
| T5 | `strong` issue, questions, resume | | |
| T6 | Iterating on the MR, human push | | |
| T7 | No triggers from bot / chatter | | |
| T8 | Queueing | | |
| T9 | Idle, hibernate, archive, wake up | | |
| T10 | Finishing an issue | | |
| T11 | Run limit | | |
| T12 | Failure handling | | |
| T13 | Credential isolation, probes | | |
| T14 | Network policy (optional) | | |
| T15 | Helm upgrade and uninstall | | |
| T16 | Usage limits | | |
| T17 | CI feedback loop | | |
| T18 | Autopilot milestone | | |
| T19 | Merge conflicts | | |

## 5. Cleanup

```sh
hack/sandbox/down.sh   # deletes the kind cluster
```

In GitLab, close or delete the sandbox issues, MRs and `agent/*` branches,
and revoke the access token when you are done.

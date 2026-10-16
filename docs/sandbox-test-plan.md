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
The sandbox limits are: `maxConcurrentRuns: 1`, `idleTimeout: 10m`,
`retentionPeriod: 1h`, `maxRunsPerTask: 6`, small `maxBudgetUSD`,
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
* `kubectl -n agents logs <pod> -c runner` shows "starting run" and "run finished".
* A few (likely 3) new issues in the milestone with the labels
  `ai-generated`, `agent::draft`, `agent-profile::fast|strong`. Each holds a
  self-contained sub-spec (context, plan with real file names such as
  `src/server.js`, acceptance criteria, test plan), `Depends on #…` where it
  makes sense, and a question about tag normalisation with a suggested
  default.
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
* The runner log shows the profile `fast` with its engine and model.
* The reviewers run (two more agent invocations in the agent log).
* Branch `agent/<iid>-<slug>` is pushed, and a **draft MR** "Draft: <title>"
  with `Closes #<iid>` and the summary is opened. The issue gets a comment
  linking the MR, and the label becomes `agent::review`.
* `npm test` passes on the branch (check out the MR locally or look at CI if
  you add one), and new tests exist.
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

1. After its run, the task is `Idle` and the pod keeps running.
2. After 10 minutes (`idleTimeout`): the task is `Hibernated`, the pod is
   gone, and the PVC `<task>-ws` still exists.
3. Comment `/agent add a test for an empty tag list`.

**Expected:** the pod comes back, the run resumes the session (the PVC kept
`$HOME`), and the push and MR comment work.

4. After 1 hour without activity (`retentionPeriod`): the task is `Archived`
   and the PVC is gone.
5. Comment `/agent …` again.

**Expected:** a new PVC and pod. The runner state and the agent session were
on the deleted PVC, so the run starts a **fresh session** with the full issue
thread, clones the repository again, and continues on the existing branch
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

On any running pod:

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

## 5. Cleanup

```sh
hack/sandbox/down.sh   # deletes the kind cluster
```

In GitLab, close or delete the sandbox issues, MRs and `agent/*` branches,
and revoke the access token when you are done.

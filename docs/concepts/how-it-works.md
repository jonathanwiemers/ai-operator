# How it works

This page follows a milestone from the spec you write to merged and released
work. Read it to know what happens when, who does it, and where you come in.
It explains the ideas; the guides show the steps.

## The big picture

<p align="center">
  <img src="../assets/architecture.svg" alt="You write the spec in GitLab and steer from the Desk, mostly by talking to the pilot. The operator in your cluster starts one pod per task, and the agents in the pods call the models and push to GitLab" width="720">
</p>

There are four parts:

- **GitLab** holds the work. You write the [spec](glossary.md#spec) in a
  [milestone](glossary.md#milestone), and the issues, merge requests and
  pipelines live there.
- **The operator** runs in your Kubernetes cluster. It watches your GitLab
  project, keeps a queue per milestone, and starts a pod for each piece of
  work. It follows fixed rules, not a model: it decides when an issue starts
  and when a merge request merges, and it cuts the releases that were
  planned.
- **The agents** run in those pods. They are coding agent CLIs (Claude Code
  or Cursor) that plan, implement and review. They call the model providers
  and hand their commits to the operator's side of the pod, which pushes them.
- **The [Desk](glossary.md#desk)** is the optional web UI where you steer: you talk
  to the [pilot](glossary.md#pilot), chat with an agent, answer
  [prompts](glossary.md#prompt), revisit decisions, and watch runs, CI and
  costs.

You describe the project to the operator once, in an
[`AgentProject`](glossary.md#agentproject) resource: which GitLab project, which tokens, which models, how many runs at
a time. From then on you work in GitLab and the Desk.

## From spec to merged work

The main path is [autopilot](glossary.md#autopilot): you write the spec, and
the milestone runs end to end while you steer.

1. **You write the spec.** The milestone description holds the goal, the
   requirements and the decisions, plus a few
   [marker lines](glossary.md#marker-line) such as `/agent spec`,
   `/agent autopilot` and `/agent owners @you`. The
   [owners](glossary.md#owner) are the people the operator mentions and
   asks.
2. **A tracking issue opens.** The operator creates a
   [tracking issue](glossary.md#tracking-issue) for the milestone at once.
   It shows the queue and its state, and it is where the pilot lives. A
   project works on one autopilot milestone at a time: a younger milestone
   waits for its turn unless an owner starts it first.
3. **The planner plans.** When its turn comes, the
   [planner](roles.md#planner) reads the spec and the code and writes one
   [sub-issue](glossary.md#sub-issue) per merge request, each with a short
   spec, acceptance criteria, its dependencies and whether a release follows
   it. On autopilot it asks no questions: it takes the default it would
   propose and records it as a [decision](glossary.md#decision).
4. **Issues start in dependency order.** An issue starts once every issue it
   depends on is merged and a slot is free. Several issues of a milestone can
   run at once, up to the milestone's limit (`autopilot.maxParallel`, or
   `/agent parallel <n>` in the milestone description). The
   Desk groups the plan into [waves](glossary.md#wave), but nothing waits
   for a whole wave to finish.
5. **The implementer works.** Each issue gets its own
   [task](glossary.md#task) and pod. The [implementer](roles.md#implementer)
   works in steps on a draft merge request, so you can follow and steer it.
   Each step is a commit.
6. **Reviewers check every change.** The [reviewers](roles.md#reviewers)
   read the diff for bugs and for security problems. Blocking findings go
   back to the implementer.
7. **CI goes green.** The operator follows the pipeline, retries flaky jobs
   and hands real failures back to the agent. Watching a pipeline costs no
   tokens.
8. **The operator merges.** Once CI is green, the review is clean and
   nobody else pushed, the merge request passes the merge gate. Merge
   requests of one milestone land one after another through the
   [landing lane](glossary.md#landing-lane): each is rebased onto the
   latest default branch when it needs it, checked by CI on that head and
   merged. A model never decides to merge.
9. **Releases follow the plan.** At each
   [release point](glossary.md#release-point) the operator tags a release
   once the issue before it is merged. See [Planned releases](#planned-releases).
10. **Autopilot is done.** When every issue is merged or skipped, the
    operator decides on the milestone's final release (see
    [Planned releases](#planned-releases)) and waits for any
    [companion](glossary.md#companion-repository) merge requests to land.
    Then autopilot is done, and the pilot writes the
    [retrospective](glossary.md#retrospective) on the tracking issue: what
    was merged, the decisions, the pauses, cost and time, and follow-ups you
    can open as new issues.
11. **The milestone closes.** The operator closes the GitLab milestone once
    the retrospective's follow-ups are settled (opened, dismissed, or their
    time to open them has passed) and no other issue or companion of the
    milestone is still open.

### When something goes wrong

Autopilot stops where a rule says it cannot go on safely: red CI it could
not fix, a failed run, review findings that stay, a spent
[budget](glossary.md#budget), a red default branch.

- When the milestone's parallel limit is above 1, a problem of one issue
  puts a [hold](glossary.md#hold) on that issue, even if it is the only
  issue running. The other issues go on, and issues whose dependencies are
  merged still start. If every running issue is held and nothing else can
  start, for example because the next issues depend on a held one, the
  milestone pauses as `stuck`. It goes on by itself once an issue can start
  or a hold ends.
- A problem of the whole milestone, or any problem when the limit is 1, is
  a [pause](glossary.md#pause). Nothing new starts until it ends.

The [pilot](roles.md#pilot) looks at each pause. It fixes what it may fix on
its own, such as starting a run again, and asks the owners in a
[prompt](glossary.md#prompt) when it needs a decision. Many holds and pauses
end by themselves once their cause is gone, for example when a later run
gets CI green.

A run that hangs without any output for a long time is noticed by the
operator's [watchdog](glossary.md#watchdog). The pilot can restart it, and
an owner can restart a run in the Desk, with *Restart run* on the task's
page. There is no `/agent` command for it in GitLab, but a restart leaves a
note on the issue (or the tracking issue) saying who restarted the run and
in which step. Only a planning run of a milestone without autopilot is
restarted without a note.

### Steering it

You do not have to wait for a pause to have a say:

- **Ask the pilot** in the Desk, or with `/pilot` in a comment on the
  tracking issue. It knows the spec, the queue, every issue's state and the
  health of the default branch.
- **Chat with an agent** on a task's page in the Desk, or give it an
  instruction with `/agent <what to change>` on its issue or merge request.
- **Answer prompts** in the Desk. A prompt is a question to the owners with
  two to four options, one of them recommended, from the operator, the pilot
  or an agent.
- **[Revisit](glossary.md#revisit) a decision**, even after the merge. A
  run takes your change into account and records it.
- **Pause, resume or skip** with `/agent pause`, `/agent resume` and
  `/agent skip #N` on the tracking issue, or with the buttons in the Desk.

### Without autopilot

You can also hand issues over one by one with labels. Write the spec with
`/agent spec` only. The planner writes the sub-issues as drafts
(`agent::draft`) and may ask you questions in them. You answer, then set
`agent::ready` on an issue to hand it over. The implementer opens a draft
merge request, gets CI green and sets `agent::review`. You review, ask for
changes with `/agent <what to change>`, and merge yourself.

The roles, the reviews, the CI loop, the pods and the split between runner
and agent container work the same way. What only autopilot does: the queue
on a tracking issue, the merge gate and the landing lane, holds and pauses,
planned releases and the retrospective. Without autopilot a milestone has no
pilot either, unless you ask for one with `/pilot coordinate` on one of its
issues.

## Pods per issue

Every piece of work is a task, and every task gets its own pod and its own
volume:

- one task per planned milestone, for the planner;
- one task per issue, for the implementer and the reviewers of that issue;
- one task per milestone with a tracking issue, for the pilot.

Each time an agent does something, that is a [run](glossary.md#run): a
planning run, an implementing run, a review, a CI fix or a chat turn. Shortly
after a run the pod stops, so it holds no CPU or memory between runs. The
volume stays, with the checkout, the build caches and the agent's session,
so the next run picks up where the last one ended. A volume that sits
unused for long is removed too; the next run starts from a fresh clone and
the issue thread.

The operator caps how many runs of a project use tokens at the same time.
Extra runs wait in a queue: a fix of a red default branch first, then runs
that only review a change again, then the rest, older milestones first. A
run that has started is never stopped for one that ranks higher. The pilot
is outside that queue. When the issue
is done, its task, pod and volume are deleted.

## The trusted runner and the agent container

Each pod has two containers that trust each other very differently:

- **The runner** is trusted. It holds the GitLab token, talks to GitLab,
  keeps its own copy of the repository and pushes. It never runs the
  model's code.
- **The agent container** is not trusted. It runs the agent CLI with the
  model's credentials and the checkout it works on. It has no GitLab token
  and no cluster credentials.

The two exchange commits only as git bundles, a file format that carries
commits and nothing else. The runner checks what comes back before it
pushes, and it only pushes to `agent/` branches. So even an agent that
follows instructions planted in an issue cannot get at your GitLab token or
push to the default branch. [Security](security.md) explains this in more
detail.

## Planned releases

A merge alone releases nothing. A release is a decision:

- **The planner** decides per issue whether a release follows it, and writes
  it into the issue as a `**Release:**` line. That is a release point.
- **The pilot** may add or drop a release point while the milestone runs,
  with a reason.
- **An owner** cuts a release in between with `/agent release`.
- **The milestone's final release** is decided once the queue is through.
  It is cut only if it is needed: a milestone that merged nothing, or whose
  changes since the last release are not release-relevant (docs only, for
  example) or are already in a release, ends without one.

The operator carries the decisions out when the queue gets there: it
creates the tag and the GitLab release on the newest head of the default
branch whose pipeline is green and that contains the merge of the issue
before it. Since the queue does not wait for a release, later merges may
have landed by then, so the tag is often on a later commit than that merge.
Your project's pipeline for the tag builds what it builds. Each release is announced on the
tracking issue with release notes.

## Next

- [Roles](roles.md): the planner, implementer, reviewers, pilot and the
  helpers, and what you see of each.
- [Security](security.md): what the model can and cannot reach.
- [Glossary](glossary.md): every term on this page in one or two sentences.
- [Getting started](../getting-started/README.md): install the operator
  and plan your first milestone.

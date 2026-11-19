# Write a milestone

A [milestone](../concepts/glossary.md#milestone) is one feature: its
description is the [spec](../concepts/glossary.md#spec) for the whole of it,
and the [planner](../concepts/glossary.md#planner) splits it into issues,
one per merge request. This page shows how to write that spec, which
[marker lines](../concepts/glossary.md#marker-line) to add, what the planner
makes of it, and how the order of the issues is kept.

You can draft the spec with an agent in the [Desk](../concepts/glossary.md#desk),
or write it in GitLab yourself. Either way it ends up as the description of
a GitLab milestone, and that is where it lives.

## Draft it in the Desk

With Desk chat on, an owner of the project can plan a milestone with an
agent that reads the project's default branch, its milestones and its
issues:

1. Open the project's *Plans* tab and click *Plan milestone*. Or open
   *Plans* in the rail, click *Plan milestone* and pick the project; with
   one project you own, there is nothing to pick.
2. Tell the agent what you want in the chat on the left. It writes the
   draft on the right, and you can edit the draft yourself with *Edit* and
   *Save*. The next chat turn sees your edit.
3. Click *Create milestone*. The dialog shows the title, the final draft,
   the switches *Spec* and *Autopilot*, the `/agent` lines it adds (with the
   project's owners) and an estimate of the cost.

The operator then creates the milestone in GitLab with your GitLab login.
A plan has its own budget; the chart values are in the
[chart README](../../charts/ai-operator/README.md#plan).

### Answer the agent's decisions

The agent returns its decisions as a list, not only as prose in the draft.
Each plan decision (like a [decision](../concepts/glossary.md#decision) on
an issue) has an ID (`D1`, `D2`, …), a question, a few options, the option
the agent recommends and why. It is *open* while it is a question in the
agent's reply, and *locked* once it is in the draft's
`## Decisions (locked)` section.

On the plan page, the button reads **Answer 3 questions** while three
decisions are open, and **Review decisions** otherwise. It is in the
*Decisions* tab and in the draft's toolbar. It is for owners, and it is off
while a turn runs and once the milestone is created.

A review goes like this:

1. Click **Review decisions**. The wizard opens on the first decision that
   is open or not signed off. A phone shows it full screen.
2. Each step shows one decision. The agent's pick is preselected and marked
   *Recommended*; a decision you signed off shows your choice instead. Keep
   it, pick another option, or choose *Other* and write your own answer.
3. *Back*, *Next* and the step list move between steps ("3 of 7").
4. The last step lists every decision as *kept* or *changed*. Add text for
   the agent if you like, then click **Send**.

The answers go to the agent as one chat message. The chat shows it as
"Answered 7 decisions: 5 kept, 2 changed", with the changed ones listed
and your text below. The agent's next turn updates the draft and the list.
Your picks stay in your browser until a send succeeds. If you edited the
draft since the agent's last turn, the wizard warns you and still sends; the
agent reads your edit with the answers.

If another owner sent first, the decisions changed under you. The wizard
says so and reloads them, and it keeps your picks of the decisions that did
not change. Answers in a turn that fails or that you interrupt are not
applied: send them again.

### The Decisions tab and sign-off

The *Decisions* tab shows every current decision. On a phone the plan has
the tabs *Chat*, *Decisions* and *Draft*. On a wider screen the chat stays
on the left and the right pane has *Draft* and *Decisions*.

A card shows the state, the question, the options with the recommended or
chosen one marked, and why. Under it, one line says who decided and who
signed off, for example "Decided by the agent · signed off by Sam". Open
*History* on a card to see every version and each
[sign-off](../concepts/glossary.md#sign-off) with its time.

A decision gets a new version each time its title, question, options,
recommendation or state changes. These rules say who decided and who signed
off:

- You **keep** a decision: you sign off its current version. A second
  owner's keep adds a second sign-off.
- You **change** a decision and the agent applies it: you decided the new
  version and signed it off.
- The agent changes a decision on its own, for example when you ask it in
  the chat to rethink D3: the new version is decided by the agent and shows
  *Not signed off* until an owner keeps it. The wizard leads to it first.
- You change a decision and the agent does not apply it: nothing is signed
  off. The agent says why in its reply.

*Create milestone* warns "2 decisions are open or not signed off" and still
creates the milestone. The milestone's description is the draft as it is,
with no sign-off notes. The history expires with the plan's chat history.

### Find your plans

*Plans* in the rail lists the plans of every project you may see, with the
project's name. A project's *Plans* tab lists only its plans. Both show
*Drafting* first, then *Milestones created*, each last active first. A row
shows the title, the phase, the spend of the plan's budget and the last
activity.

The agent names a new plan after your first message. Once the milestone
is created, the plan takes the milestone's title and keeps it. A plan you
started with a title through the API keeps that title until then.

A plan that created a milestone links to it as *%N*, with the milestone's
state (running, waiting, paused, planned or done) and how many of its
issues are merged. When the operator does not plan that milestone, for
example because you switched *Spec* off, *%N* opens it in GitLab and the
row says *Not planned*.

## Write it in GitLab

In GitLab, open *Plan > Milestones*, create a milestone and write the spec
into its description:

```markdown
/agent spec
/agent autopilot
/agent owners @alice
/agent owner-subjects alice@example.com

# Checkout: customers can pay with PayPal

## Goal

Customers can pay with PayPal in the new checkout.

## Requirements

- **R1** A PayPal button on the payment step starts the PayPal flow.
- **R2** A paid order shows "Paid with PayPal" on the order page.

## Constraints

- No new dependencies.
- Run `make test` before you push.
```

The planner starts within a poll interval, and the milestone shows up in
the Desk. [Your first milestone](../getting-started/first-milestone.md)
walks through what you see then.

## What a good spec says

On [autopilot](../concepts/glossary.md#autopilot) nobody answers questions
once the work starts: every gap in the spec becomes a default the agents
pick. So say what you want, what you already decided, and what is out of
scope. Write what and why, not how. Short, numbered requirements give the
planner a clear split.

A spec that works well has these sections:

| Section | What goes in |
|---------|--------------|
| Goal | What a user can do when the milestone is done. |
| Background | What exists today, so the planner does not guess. |
| Decisions | What you already decided, with the options you rejected. |
| Requirements | Numbered, one outcome each (`R1`, `R2`, …). |
| Constraints | Rules every merge request follows, such as a check to run. |
| Non-goals | What is out of scope, so no agent adds it. |
| Acceptance | How you will tell the milestone is done. |

The [`milestone-spec` skill](../../skills/milestone-spec/SKILL.md) is the
house style for these specs, with a
[template](../../skills/milestone-spec/template.md) to copy and a checklist.
A coding assistant that reads skills, such as Claude Code, can use it to
write or review a spec with you.

The project's house rules, such as the size budget of a merge request, are
added to the planner's constraints (see
[`spec.houseRules`](../reference/agentproject.md#spechouserules)). A
milestone's own constraints win over them.

## Marker lines

Marker lines start with `/agent` and tell the operator what to do with the
milestone. They can stand anywhere in the description; the top is easiest
to find.

- **`/agent spec`** plans the milestone.
- **`/agent autopilot`** plans it and runs it end to end. This is the main
  way to run a milestone; see [Autopilot](autopilot.md#a-milestone-end-to-end).
- **`/agent owners @alice`** names the [owners](../concepts/glossary.md#owner)
  the operator mentions in GitLab (see
  [Owners and pings](owners-and-pings.md)).
- **`/agent owner-subjects alice@example.com`** names who may answer the
  milestone's prompts in the Desk.

More lines set a budget, the number of issues that run at once, standing
rules and [companion repositories](../concepts/glossary.md#companion-repository).
Every line is in [Commands](../reference/commands.md#in-the-milestone-description).

## What the planner makes

The planner reads the spec and the repository and plans one
[sub-issue](../concepts/glossary.md#sub-issue) per merge request. The
operator creates them in the milestone. Each sub-issue holds a spec of its
own:

- the part of the milestone it implements, with context and a plan;
- acceptance criteria and a test plan;
- a *Depends on* list with the issues that must be merged first (see
  [Dependencies](#dependencies));
- a **Release:** line: `after this issue: <why>` when the change is a step
  users notice or must be live before the next one, else
  `not after this issue`. A release without a reason counts as none (see
  [Planned releases](../concepts/how-it-works.md#planned-releases));
- *Open questions*, each with the default the agent would pick;
- the labels `ai-generated`, `agent::draft` and the
  [profile](../concepts/glossary.md#profile) the planner chose, such as
  `agent-profile::fast`.

The planner records the choices it made for each issue as
[decisions](../concepts/glossary.md#decision) in a comment on that issue.
On autopilot it asks no questions: its defaults show up there. You can
change each of them later, see
[Decisions and Revisit](iterate-and-finish.md#decisions-and-revisit).

Without autopilot, the drafts wait for you: read them, answer the open
questions and hand them over (see
[Answer and hand over](answer-and-hand-over.md)).

## Change the spec later

When you edit the spec, the planner runs again. It updates the issues that
are still drafts and leaves the others alone: issues in progress, done, or
written by hand. It keeps the release decision of an issue that is no draft
any more.

Changing `owners`, `owner-subjects`, a rule, the budget or `parallel` does
not plan again. The spec text and `repo` lines do.

The operator never rewrites your spec on its own. It writes into the
description only when an owner asked for it: the pilot's
[spec changes and rules](pilot.md#spec-changes-through-the-pilot) after
an owner's `yes`, a `parallel N` command, or a budget raise an owner picked
in a prompt.

## When planning fails

If planning fails for an outside reason, such as a GitLab or network error
or a lost pod, the operator plans once more, after
[`lifecycle.infraRetryAfter`](../reference/lifecycle-and-ci.md#speclifecycle).
Issues a failed run created stay, and the next run finds them again.

Any other failure, or a retry that fails too, waits for you: change the
spec to plan again. On autopilot every planning failure is a comment on the
milestone's [tracking issue](../concepts/glossary.md#tracking-issue) with
the error and whether a retry follows, and it mentions the owners. Without
autopilot, the task's status and the spec pod's log have it (see
[Watch from Kubernetes](watch-from-kubernetes.md)).

## Dependencies

The planner orders the work: an issue that needs another one lists it under
*Depends on*. On autopilot, issues with no dependency between them may run
at the same time, so the planner keeps them off the same files. Where they
would overlap, it makes one depend on the other. Conflicts that happen
anyway are
[resolved by the agent](while-the-agent-works.md#merge-conflicts).

The dependencies are also GitLab issue links, so the issue page and the
boards show them:

- On **GitLab Premium and Ultimate**, an issue is linked as *is blocked by*
  the issues it depends on.
- On **GitLab Free**, blocking links do not exist. The operator uses
  *relates to* links, which have no direction, and the *Depends on* list
  stays the record of the order.

A re-plan adds links for new dependencies and removes the links it made
for dependencies it dropped, on drafts only. Links you made stay, and a
link of the plan you removed stays removed. The run log's
`link dependencies` step says which link type was used.

**Order work yourself.** On autopilot, an *is blocked by* link you add
(Premium) holds an issue back until the linked issue is closed, even if the
plan does not list it, and even if the linked issue is in another project.

## Next

- [Answer and hand over](answer-and-hand-over.md): answer the planner's
  questions and start the work.
- [Commands](../reference/commands.md#in-the-milestone-description): every
  marker line.
- [Your first milestone](../getting-started/first-milestone.md): a short
  spec on autopilot, step by step.
- [Desk API](../reference/desk-api.md#plans): the plan endpoints, the
  decisions history and the `decisions` field of the plan chat.

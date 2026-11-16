# Your first milestone

This page runs your first [milestone](../concepts/glossary.md#milestone) on
[autopilot](../concepts/glossary.md#autopilot). You write a short
[spec](../concepts/glossary.md#spec) in GitLab, watch the
[planner](../concepts/glossary.md#planner) turn it into issues in the
[Desk](../concepts/glossary.md#desk), answer a
[prompt](../concepts/glossary.md#prompt), and see the first merge request.

It builds on [Install](install.md) and [Turn on the Desk](turn-on-the-desk.md):
the operator watches your project, and you can log in to the Desk.

On autopilot the operator merges each merge request once CI is green and
the review is clean, without asking you. For a first try, pick a small
change, or a test project.

## 1. Write the spec

In GitLab, open your project's *Plan > Milestones* and create a milestone.
Give it a title, and put the spec into its description:

```markdown
/agent spec
/agent autopilot
/agent owners @alice
/agent owner-subjects alice@example.com

# Dark mode

## Goal

Users can switch the shop between a light and a dark theme.

## Requirements

- **R1** A toggle in the page header switches the theme. The choice is kept
  in the browser's local storage and applied on the next visit.
- **R2** The dark theme keeps text readable (WCAG AA contrast) on every page.

## Constraints

- No new dependencies.
- Run the existing tests before you push.
```

The first four lines are [marker lines](../concepts/glossary.md#marker-line):
lines that start with `/agent` and tell the operator what to do with the
milestone.

- **`/agent spec`** plans the milestone: the planner splits it into
  [sub-issues](../concepts/glossary.md#sub-issue), one per merge request.
- **`/agent autopilot`** runs it end to end: each issue is implemented,
  reviewed and merged, and the next one starts once the issues it depends
  on are merged. The planner asks no questions on autopilot. It takes a
  sensible default for each open point and records it as a
  [decision](../concepts/glossary.md#decision).
- **`/agent owners @alice`** names the
  [owners](../concepts/glossary.md#owner) in GitLab: the people the
  operator mentions when it needs a human. Use your GitLab username.
- **`/agent owner-subjects alice@example.com`** names the owners in the
  Desk: who may answer this milestone's prompts. Use the user name at the
  bottom of the Desk's rail (see [Log in](turn-on-the-desk.md#5-log-in)).

Every marker line, and what else you can set per milestone, is in
[Commands](../reference/commands.md#in-the-milestone-description).

The rest is the spec. Write what you want and why, not how. Short,
numbered requirements give the planner a clear split.

## 2. Watch it get planned

Open the Desk. Your milestone shows up on *Overview* on the operator's next
poll, with the state `running`. Click its title to open it.

The milestone page opens on the *Issues* tab. It says *No issues planned
yet.* while the planner works. In GitLab, the operator opens a
[tracking issue](../concepts/glossary.md#tracking-issue) for the milestone
right away. It holds the milestone's status, and you can comment there.

When the planner is done, the *Issues* tab lists the planned issues, each
with its state and, once it has one, its merge request. The *Queue* below
groups them by where they are: *Running*, *Waiting* for the issues they
depend on, and *Done*. In GitLab the same issues appear in the milestone,
each with an `agent::` label (see [Labels](../reference/labels.md)).

If the milestone shows *Waiting for its turn*, an older autopilot milestone
of the project is still open. Autopilot runs one milestone at a time. To
plan this one now anyway, comment `/agent start` on its tracking issue.

## 3. Answer a prompt

A prompt is a question to the owners with two to four options, one of them
recommended. The [pilot](../concepts/glossary.md#pilot), which watches the
milestone for you, asks one when it needs a decision: for example when CI
stays red, or a budget runs out. Every open prompt is in your *Inbox* and
on the milestone's *Pilot* tab.

You do not have to wait for a problem to try it. In the *Queue*, a planned
issue lists the decisions the planner made for it, as IDs such as
`D12-P1`, each with a **Revisit** button.
[Revisit](../concepts/glossary.md#revisit) asks you whether a decision
should stay:

1. Click **Revisit** next to one of the decisions. The Desk says
   *Revisit of D12-P1 opened.*, and a link *Go to Q1* appears.
2. Follow the link. The *Pilot* tab shows the prompt *Revisit D12-P1: …?*
   with its options: keep the decision (recommended), or switch to one of
   the alternatives the planner rejected.
3. Click the option to keep the decision. The card then says
   *Answered by alice@example.com in the Desk: Keep …*, and nothing
   changes.

Had you picked an alternative, the operator would have added a follow-up
issue to the milestone that changes it. Instead of an option you can also
write what you want in your own words; the pilot then turns it into an
option for you to confirm.

If you see no **Revisit** button, check that your user name is on the
`/agent owner-subjects` line and that your access rule has `chat: true`.
A prompt that only shows *Only the milestone's owners can answer.* has the
same cause.

The operator also posts each prompt in GitLab, on the issue it is about or
on the tracking issue, with a link that opens it in the Desk. You can answer
there with a comment as well.

## 4. See the first merge request

The first issue starts as soon as it is planned. Its
[implementer](../concepts/glossary.md#implementer) works on a branch and
pushes its steps to a draft merge request. On the *Issues* tab the issue's
*MR* column then links it, for example `!7`. Click the issue to open its
task page: it shows the merge request, the run as it happens, the CI
pipeline and what the run cost.

When the implementer is done, the [reviewers](../concepts/glossary.md#reviewer)
check the merge request, and the implementer fixes what they find. Once CI
is green and the review is clean, the operator merges it: this rule is the
[merge gate](../concepts/glossary.md#merge-gate), and no model decides to
merge. The issue moves to *Done*, and the issues that waited for it start.

When the last issue is merged, the milestone is done, and the pilot writes
a [retrospective](../concepts/glossary.md#retrospective) on the
*Retrospective* tab.

If nothing happens for a while, check the tasks from the cluster:

```sh
kubectl -n agents get agenttasks
kubectl -n agents describe agentproject shop
```

## Next

- [Commands](../reference/commands.md): every `/agent` and `/pilot`
  command, and the marker lines.
- [How it works](../concepts/how-it-works.md): what happens between spec
  and merge.
- The guides guides/write-a-milestone.md, guides/autopilot.md and
  guides/steer-from-the-desk.md, written later, go deeper into each step.
  Until then see [Usage](../usage.md).

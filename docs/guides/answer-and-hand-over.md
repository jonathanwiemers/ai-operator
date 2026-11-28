# Answer and hand over

The agent works on its own most of the time, but sometimes it needs you.
The [planner](../concepts/glossary.md#planner) leaves open questions on a
draft issue, and the [implementer](../concepts/glossary.md#implementer)
stops with questions when a design choice is unresolved. This page shows how
you answer them, in the [Desk](../concepts/glossary.md#desk) first and in
GitLab second, and how an issue is handed to the agent.

On [autopilot](../concepts/glossary.md#autopilot) you hand nothing over:
each issue starts on its own once the issues it depends on are merged. The
hand-over by label at the end of this page is for milestones without
autopilot and for issues you wrote yourself.

## Answer the agent's questions in the Desk

When the implementer has questions, the issue moves to
`agent::needs-input`, and each question becomes a
[prompt](../concepts/glossary.md#prompt) with a number such as `Q5`. A
question about a choice has two to four options, the agent's default
marked as recommended. An open question has a text box only.

1. Open the *Inbox*. Each open question is a row; click it.
2. The issue's task page opens on its *Overview* tab, at *Questions from
   the agent*. To tell the agent more, message the pilot in the
   milestone's *Cockpit*.
3. Click an option, or write your answer in your own words.

Once you answered the last open question of the run, the run starts again,
and the issue moves back to `agent::working`. The agent gets all your
answers in one turn, as your words: "Answer to your question Q5 (…) from
@alice: …". Each prompt's GitLab comment then shows the answer and who gave
it.

Only the milestone's owners in the Desk may answer: the names on its
`/agent owner-subjects` line (see
[Commands](../reference/commands.md#in-the-milestone-description)). That
line is separate from `/agent owners`, which names whom GitLab notifies.
There is no fallback: without the line, nobody can answer in the Desk.
Anyone else sees *Only the milestone's owners can answer.* A question is
answered once; a second answer is refused and names the first.

A new run that starts before you answer, for example from a comment or a
retry, closes the open questions. The agent then reads your comments
instead.

### Answer in the Cockpit

The pilot's and the operator's questions about a milestone wait on the
milestone's [pinned cards in the *Cockpit*](desk.md#the-pinned-cards), in the issue's
[problem thread](pilot.md#the-problem-thread). The open question's options
sit at the bottom of the thread. Click an option or write your answer, then
send it. Your answer shows in the thread at once, *sending* and then *sent*;
if the Desk refuses it, it says *not sent* with the error and the options
come back.

Once the question is closed, the thread keeps it as one entry: the question,
what was chosen and by whom, and the outcome.

### How a question closes

Every closed question says who closed it and why:

| Label | Meaning |
|-------|---------|
| *Answered by @login* | An owner answered, with the choice. |
| *Handled by the pilot* | The pilot closed it, with the cause. |
| *Resolved on its own: <cause>* | The cause went away: main is green again, the issue was merged or closed, the agent ran again without the answer, the budget is no longer short, the pause ended, CI is green again so the hold lifted, or the milestone moved on. |
| *Replaced by Q14* | A newer question of the same kind and subject took its place. The label links to it. |
| *Expired: nobody answered in time* | The proposal ran out of time. |

The question's GitLab comment shows the same words. Questions that closed
before the Desk recorded the reason keep their older text.

## Answer in GitLab

The operator also posts each question on the issue, as a comment with a
link that opens it in the Desk. To answer in GitLab instead, comment
`/agent <your answer>` on the issue or its merge request. This starts a new
run that reads your comment, and the open prompts close. A reply with an
option's letter does not answer an implementer's question; letter replies
work only on a milestone's prompts (see
[Answers in a prompt's thread](../reference/commands.md#answers-in-a-prompts-thread)).

A comment without `/agent` starts nothing. The agent reads it with its next
run.

With `workflow.inputs: [desk]` the project takes answers in the Desk only
(see [`spec.workflow`](../reference/agentproject.md#specworkflow)).

## The planner's open questions and decisions

On autopilot the planner asks no questions. It picks a default for each
open point and records it as a [decision](../concepts/glossary.md#decision)
in a comment on the issue. To change one before the issue is merged,
comment `/agent revisit D12-P1 <what you want instead>` on the issue: its
next run applies it. After the merge, the **Revisit** button next to the
ID in the milestone's *Issues* tab does it (see
[Decisions and Revisit](iterate-and-finish.md#decisions-and-revisit)).

Without autopilot, each draft issue lists its *Open questions* in the
description, each with the default the planner would pick. Answer them as
comments on the issue, or edit the description. The agent reads both when
its first run starts. A default you leave alone is what the agent does.

## Hand an issue over by label

Without autopilot, an issue waits as `agent::draft` until you hand it over.
This is also how you give the agent an issue you wrote yourself.

1. Check the [profile](../concepts/glossary.md#profile) label. Change
   `agent-profile::fast` to `agent-profile::strong` if the issue is harder
   than the planner thought, or the other way round. Without a profile
   label, the issue runs on `agents.implement` (see
   [Profiles](../reference/agents-and-models.md#profiles)).
2. Set the label **`agent::ready`**.

The next poll starts a run: the label becomes `agent::working`, and the bot
user is assigned if the issue has no assignee. An issue needs no milestone,
unless the project sets `milestones.titlePattern` (see
[`spec.milestones`](../reference/agentproject.md#specmilestones)).

To have the operator merge such an issue too, take it into the [loose issues queue](loose-issues.md). Every label and what moves it is in [Labels](../reference/labels.md). What
happens next is in [While the agent works](while-the-agent-works.md).

## Next

- [While the agent works](while-the-agent-works.md): the draft merge
  request, its steps, CI and conflicts.
- [Iterate and finish](iterate-and-finish.md): change requests, decisions
  and merging.
- [Owners and pings](owners-and-pings.md): who gets mentioned, and who may
  command.

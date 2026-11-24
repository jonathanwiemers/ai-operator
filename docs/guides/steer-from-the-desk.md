# Steer from the Desk

The [Desk](../concepts/glossary.md#desk) is where you steer a milestone: you
chat with an agent, ask the [pilot](../concepts/glossary.md#pilot), answer
[prompts](../concepts/glossary.md#prompt), change a decision, end a pause,
order the queue and plan the next milestone. This page shows each of these.
What the views show is in [The Desk](desk.md).

Most actions need the `chat` grant. Some also need your Desk user name
among the milestone's [owners](../concepts/glossary.md#owner), on its
`/agent owner-subjects` line: answering prompts, *Revisit*, *Move to top*, *Merge anyway*, *Restart run*, and planning or follow-up
milestones. See
[Who sees what](desk.md#who-sees-what).
Everything you do here, you can also do with a comment in GitLab; the
[commands](../reference/commands.md) list both.

## Chat with an agent

![Task chat between a user and the implementing agent](../assets/screenshots/task-chat.webp)

Open a task and its *Chat* tab. *Chat with the agent* is a thread with the
agent of this task, oldest message on top.

1. Write your message and send it. No GitLab token is needed.
2. A line under the thread says what happens: the task waits for capacity,
   *starting the agent…* while its pod comes up, then *the agent is
   answering…*, with a link to the *Live* tab.
3. The agent answers in the thread. It can change and push the branch.

While the agent answers, *Interrupt* stops it. A chat message starts no
[run](../concepts/glossary.md#run) of its own.

Buttons above the box send short commands, by task type:

- **an issue's task**: *Retry*, *Review again*, and *Model…* to pick the
  engine, model and effort of the next runs;
- **a milestone's Spec task** on autopilot: *Pause* or *Resume*, and
  *Skip…* with the issue to skip;
- **the pilot's task**: *Accept* or *Reject* for each open proposal.

The command shows as a short line in the thread, with the operator's answer
under it.

**A run that hangs.** In the task page's header, owners get *Restart run*
while a run is in progress. It asks first, then stops the run and starts it
again. This is a Desk action only, there is no GitLab command for it. See
[Restart a stuck run](../reference/commands.md#restart-a-stuck-run).

## GitLab comments from the Desk

When the Desk is connected to your projects' GitLab, the *Chat* tab also
has the *Conversation*: the comments of the issue, its merge request and
the milestone's [tracking issue](../concepts/glossary.md#tracking-issue).
With the `steer` grant it has a box to write a comment in your own name. The operator reads it like
any GitLab comment: `/agent …` starts a run, a plain comment reaches a
running agent at its next step. The buttons above the box start a comment
with the common commands.

Click *Connect GitLab* in the *Conversation* once. The Desk reads with your
token, so GitLab decides what you see. *Disconnect* forgets the token;
revoke the application in GitLab to end it there too. A connection lasts 30
days.

### Set it up

1. Create an OAuth application in the GitLab your projects live on (on
   gitlab.com: *Group → Settings → Applications*, or *User settings →
   Applications*): **confidential**, scope **`api`** (GitLab has no
   narrower scope that may post comments), redirect URI
   `https://<host>/oauth2/gitlab/callback`.
2. Put its secret into the Desk's Secret under `gitlab-client-secret` (or
   into another Secret named by `ui.gitlab.existingSecret`).
3. Set the chart values:

   ```yaml
   ui:
     gitlab:
       url: https://gitlab.com
       clientID: "<application id>"
     authorization:
       rules:
         - roles: [ai-operator-admin]
           steer: true
   ```

### One user, a fixed token

For a single user, the Desk can use a fixed token of that user instead of an
OAuth application: a **fine-grained personal access token** (GitLab 19.2 and
later) limited to the group with your projects:

| Boundary | Permissions |
|----------|-------------|
| group (selected membership) | `read_project`, `read_work_item`, `create_work_item` (comments on issues and merge requests are work item actions; it also allows creating issues), `read_merge_request` |
| user | `read_user` |

To create follow-up milestones and plans with it, the user also needs the
project's Planner or Reporter role. Put the token into the Secret under
`gitlab-token`, and name its owner's Desk user name:

```yaml
ui:
  gitlab:
    url: https://gitlab.com
    tokenUser: ada@example.com
```

That user is connected without OAuth; nobody else may use the token. Other
users can still connect their own accounts if `clientID` is set too. The
token expires after a year at most; a rejected token shows as an error.

### Limits

A comment is at most 10 000 characters, and you may post 10 per minute. A
comment goes only to the task's issue, its merge request or its milestone's
tracking issue. The Desk logs who posted where, never the text.

## Ask the pilot

Write into *Message the pilot* at the bottom of the milestone's *Cockpit*.
Your message and the pilot's answer show in the Cockpit's stream. Ask where
things stand, why the milestone is paused, or what an issue waits for. See
[The pilot](pilot.md).

Without the `chat` grant but with `steer`, the box posts your message as a
`/pilot` comment on the tracking issue, in your name.
Anyone with the `chat` grant can message the pilot, owner or not. With
neither grant you read the conversation in the Cockpit but cannot write.

## Answer prompts

Every question to the owners is a prompt: from the pilot, from an agent,
about the budget or about a red default branch. It has a number such as
`Q5`, two to four options with one recommended, and, where you may write
your own answer, a text box. A prompt with actions always has a "do
nothing" option.

You find prompts:

- in the Inbox, with a browser notification if you turned them on;
- on the milestone's *Needs you* tab, at the bottom of their issue's
  [problem thread](pilot.md#the-problem-thread); closed ones stay in the
  thread's history;
- in the *Activity* feed, as a short card with the recommended option;
- on a task page, above the chat, for an agent's questions;
- on the pause card of a red default branch or a spent budget.

Click an option to answer. Or write a sentence into the text box. On a
prompt with actions, your text runs nothing: the pilot turns it into one
more option, *From your answer: …*, which you confirm with a click, or it
asks one follow-up question. On an agent's question your text is the
answer. See [Answer and hand over](answer-and-hand-over.md).

A prompt takes one answer. A second one is refused and names the first.
Afterwards the card shows who answered, which option, when and where (Desk
or GitLab). A prompt also closes when its cause goes away, such as the
default branch turning green again, the issue being merged or a newer
prompt; a proposal expires after 24 hours.

**In GitLab.** The operator posts each prompt as a comment, with a link
that opens it in the Desk, and updates the comment once it is answered or
closed. Where the project takes answers from GitLab, an owner may reply
with the option's letter in the comment's thread (see
[Answers in a prompt's thread](../reference/commands.md#answers-in-a-prompts-thread)).

## Revisit a decision

Each [decision](../concepts/glossary.md#decision) in a milestone's queue
has a *Revisit* button, on the *Issues* tab and in the project's context
panel. The card of the decision on the *Decisions* tab has it too, with a
line that says what will happen. It is there for owners with the `chat`
grant, while the milestone has its pilot. The decision's ID opens its card
on the *Decisions* tab, or the comment on GitLab without the history
database.

1. Click *Revisit* on the decision.
2. A prompt opens with the agent's choice and the alternatives it recorded
   as options, and a text box.
3. Pick an alternative or write what you want instead.

This works once the decision's issue is merged: the operator then opens a
follow-up issue that makes the change. For an issue that is still open, the
card has a text box and *Comment*, or write
`/agent revisit <id> <what you want instead>` in the *Conversation* box. See
[Read and revisit decisions](decisions.md) and
[Decisions and Revisit](iterate-and-finish.md#decisions-and-revisit).

## Pause and hold cards

When autopilot [pauses](../concepts/glossary.md#pause), the milestone's
*Needs you* tab shows the pause card: why it paused, the paused issue with its
merge request and pushed head, and *Way out*: first what ends the pause by
itself, with its state, then what you can do. Each
[hold](../concepts/glossary.md#hold) of a single issue has a card of its own
with *Resume*. The Inbox, the *Activity* feed and the *Issues* tab show a short card
with *Open*, which leads to *Needs you*.

The buttons depend on the kind of pause:

- *Retry* and *Review again* run the paused issue again or ask for a new
  review;
- *Resume* ends the pause and every hold; *Resume* on a hold card lifts
  that hold only;
- *Skip* leaves an issue out;
- *Merge anyway* merges without a full review. It is for owners and asks
  first: CI, the merge request's state and approvals still apply, and it
  is refused if someone pushed since;
- links open the issue's chat, the pipeline, the merge request, the
  companion merge request or the plan.

A way out the Desk has no button for shows as the command to post in
GitLab. After a click the card shows *sent…*, then the operator's answer.
Each kind and its ways out are in [When autopilot pauses](pauses.md).

## The queue and finishing

A project's *Milestones* tab lists its open autopilot milestones in the order
they run: *Running*, *Holding for %N* (planned, waits for the first),
*Waiting to plan*, *Paused* or *Budget*, each with its issues done and its
spend. With the `chat` grant each row has:

- a drag handle and a menu with *Move to top*, *Move up* and *Move down*.
  Only the milestone's owners can move it, like `/agent move`. See
  [Order milestones and read the timeline](milestone-order-and-timeline.md#move-a-milestone).
- *Pause* and *Resume*, as on the pause card.

Below, *Finishing* lists done milestones still open in GitLab, oldest first,
with what holds each: a re-plan, the retrospective being written, follow-ups
to settle, open issues, or companions not landed. *Close issue* closes an
open issue in your name with a note, for owners with the `steer` grant and
GitLab connected. The operator then closes the milestone on its next poll.
With nothing left, the row says *Closes on the next poll*. There is no
"close anyway": close the holding issue or the milestone in GitLab instead.

## Plan a milestone

Owners with the `steer` grant draft a new milestone with an agent: *Plan
milestone* on the project's *Plans* tab, or on *Plans* in the rail after
picking the project. The steps
are in [Write a milestone](write-a-milestone.md#draft-it-in-the-desk).

- **The plan page** has the chat on the left and the draft on the right; on
  a phone they are two tabs, *Chat* and *Draft*. Edit the draft and *Save*:
  the agent sees your edit with its next turn. If the agent writes a newer
  draft while you edit, the Desk shows the difference and lets you pick
  *Use agent's draft* or *Keep mine*.
- **The plan lists** (*Plans* in the rail, and each project's *Plans* tab)
  show each plan's title, phase, spend and the
  milestone it created, with its state and progress. *Delete* on a plan
  page leads back to the project's *Plans* tab.
- **Its budget** starts at the default cap. At the cap the plan stops until an
  owner raises it, up to the highest cap.
- **Create milestone** shows the title, the draft, the switches *Spec* and
  *Autopilot*, the `/agent` lines it adds, and an estimate of the cost from
  your finished milestones. It creates the milestone in GitLab with your
  token. A plan creates one milestone only.
- **Idle plans.** After a while without a message the plan's pod stops;
  the next message picks up where it left. A plan is deleted some time
  after its last message.

The caps, the idle time and how long plans are kept, with their defaults,
are in the [chart README](../../charts/ai-operator/README.md#plan).

## Costs at a glance

![Metrics page with totals and a stacked daily spend chart per project](../assets/screenshots/costs.webp)

- A cost chip on each project, milestone and task shows its cost and
  tokens, for example `$12.34 · 1.2M tok`. Hover it to split the tokens.
  An unknown cost shows *n/a* or a dash, never $0.
- The *Metrics* tab of a project or milestone breaks the cost down, and
  *Metrics* in the rail compares projects over time.
- An issue at 80 % of its budget shows in the Inbox.

Spend and budgets are explained in [Budgets and spend](budgets-and-spend.md).

## On a phone

The Desk works on a phone. Below 760 px a bar at the bottom holds *Inbox*
(with its count), *Overview*, *Projects* (the rail as a sheet: projects,
researches, engine usage, sign out), and *Metrics* and *Research* where you
have them. The bar hides while you type.

- Tables become cards, and every button is big enough to tap.
- The context panel opens with the *Context* button in the header. On a
  milestone page, the *Issues*, *Queue* and *Decisions* buttons open the
  milestone panel on that view.
- Text that is cut off opens with a tap.
- Prompts, pause cards, chat and *Restart run* work as on a desktop.

Turn on [browser notifications](desk.md#browser-notifications) to hear of a
pause or a prompt while a Desk tab is open.

## Next

- [The pilot](pilot.md): what it does on its own, and the retrospective.
- [When autopilot pauses](pauses.md): each kind of pause and its ways out.
- [Commands](../reference/commands.md): the same actions as GitLab comments.

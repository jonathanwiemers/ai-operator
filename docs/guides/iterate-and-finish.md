# Iterate and finish

When a merge request is not what you want yet, you ask the agent for
changes. This page shows how you do that from the
[Desk](../concepts/glossary.md#desk) and with a comment, how you change a
[decision](../concepts/glossary.md#decision) the agent made, also after the
merge, and how an issue ends.

## Ask for changes

**In the Desk.** Open the issue's task page. The *Chat* tab has two boxes:

- **Conversation** posts a GitLab comment on the issue or its merge
  request, in your name. Write `/agent` and what you want changed, for
  example `/agent keep the old button for guests`. That starts a run. A
  comment without `/agent` reaches a running agent before its next step,
  and otherwise waits for the next run. The buttons above the box start a
  comment with the common commands. It needs your GitLab account connected
  once (see [Steering](steer-from-the-desk.md#gitlab-comments-from-the-desk)).
- **Chat with the agent** talks to the agent between runs. It answers in a
  chat turn and can change and push the branch. A chat message starts no
  run. The chat also takes short commands, such as *retry*, *review*, a
  model or an effort (see
  [Commands](../reference/commands.md#on-an-issue-or-its-merge-request)).

**In GitLab.** Comment on the issue or its merge request:

```
/agent keep the old button for guests, and add a test for it
```

The agent resumes its session, gets your new comments and pushes a new
version. `/agent` must be the first word: `/agents are great` starts
nothing.

What else you can do between runs:

| You do | Effect |
|--------|--------|
| comment `/agent <instructions>` | another run; the agent gets your new comments |
| discuss without `/agent` | nothing runs and nothing is spent; the agent reads it with its next step or run |
| set `agent::ready` again | another run, for example after you answered in a comment |
| push to the agent's branch yourself | the next run goes on from your commits |
| change the profile label | the next run uses that profile |
| comment `/agent use claude opus` | the next runs use that engine and model; the comment alone starts no run |
| comment `/agent effort high` | the next runs think that hard (claude only) |
| comment `/agent repo my-group/infra/cluster` | the issue changes that [companion repository](../concepts/glossary.md#companion-repository) too; the comment alone starts no run |

`use`, `effort` and `repo` have labels too, and limits such as the models
you allow. All commands are in [Commands](../reference/commands.md), the
labels in [Labels](../reference/labels.md#labels-you-may-set-yourself).

## Decisions and Revisit

Every run records the decisions the agent made that you might want
differently: data shapes, names users see, libraries, error handling,
scope cuts. Small mechanical choices are not decisions. The agent posts
them on the issue after the run:

> 📌 **Decisions** in run `shop-42#3`. Change one with `/agent revisit <id> <what you want instead>`.
>
> #### D42-3: How tags are stored
> **Question:** How are tags normalised?
> **Chosen:** lowercase
> **Why:** the shop already lowercases slugs, so search stays case-insensitive.
> **Alternatives:** keep the case as typed; store display and normalised forms

The [planner](../concepts/glossary.md#planner) posts its decisions for each
issue the same way, with IDs like `D42-P1`. On autopilot the planner asks
no questions, so this is where its defaults show up. While an issue is a
draft, a re-plan updates the planner's comment and numbers its IDs again;
the implementer's IDs never change. A long list goes on in a second
comment. Only the bot's decision comments count, so nobody can add or fake
a decision with a comment of their own. The reviewers get the planner's
decisions with the issue.

To read them without GitLab, open the milestone's or the project's
*Decisions* tab in the Desk: every decision as a card, with search and its
chain of revisits. See [Read and revisit decisions](decisions.md).

### Change a decision on an open issue

Comment on the issue or its merge request, in GitLab or in the Desk's
*Conversation* box:

```
/agent revisit D42-3 keep the case as typed and add a normalised column
```

This starts a run. The agent gets the decision with your instruction,
changes the code and records a new decision. The old one is then marked
*Superseded by D42-5*.

- One revisit per comment. Several revisit comments are handled in the
  same run; for the same ID, the latest comment wins.
- `/agent revisit the currency`, without an ID, is a normal comment, and
  the report shows the right form. An ID that is not on the issue is passed
  on as a plain comment, and the report says so.
- A decision that is already superseded cannot be revisited: revisit the
  one that replaced it.
- If the agent records no decision that replaces the old one, the old one
  is not marked, and the report says so.

### Change a decision after the merge

Once an issue of a planned milestone is merged and closed, you can still
change its decisions. The operator then creates a **follow-up issue** in
the milestone that changes it.

**In the Desk.** Open the milestone's *Decisions* tab and find the
decision's card, or its *Issues* tab, where each issue lists its decision
IDs. Each has a **Revisit** button:

1. Click **Revisit**. A prompt opens on the milestone's *Needs you* tab and in
   your *Inbox*, with the decision's question, choice and why.
2. Pick an option: *Keep …* (recommended, changes nothing), or one
   *Switch to …* per alternative the decision recorded. Or write what you
   want in the text box: your words become an option *From your answer:
   revisit D42-3 asking for: …*, which you confirm with one click.

Only the milestone's owners in the Desk may revisit (its
`/agent owner-subjects`). If the issue is still open, the Desk tells you
to comment on it instead.

**In GitLab.** Comment `/agent revisit D42-3 <what you want instead>` on
the closed issue, or, on autopilot, on the milestone's
[tracking issue](../concepts/glossary.md#tracking-issue).

The follow-up is titled `Revisit D42-3: <decision title>`. It keeps the
original's profile, model and effort labels, and its description holds the
old decision, your instruction and who asked. Its agent works on top of the
merged change and records a decision that replaces the old one; the old
one is marked superseded as soon as that is posted. Without autopilot the
follow-up starts right away; on autopilot it joins the queue after the
planned issues. To keep the old behaviour after all, close the follow-up.

The operator answers every revisit it handles:

| Reply | Why |
|-------|-----|
| `Created #57 to revisit D42-3 …` | the follow-up was created |
| `#42 is still open …` | comment on the open issue; its next run changes the decision |
| `Decision D42-9 was not found on #42` | the ID is wrong |
| `… already superseded …` | revisit the decision that replaced it |
| `#57 already revisits D42-3 …` | one follow-up per decision: comment on it |
| `… not in this milestone` / `… does not exist` | the ID names another issue |

Each revisit is handled once. On a closed issue only comments written after
it was closed count: a revisit written while it was open belongs to its
runs. On the tracking issue, revisits count while autopilot is on, also
after the milestone is done. A re-plan leaves follow-ups alone. Milestones
without `/agent spec` or `/agent autopilot` are not watched; open an issue
by hand there.

## Finish

**On autopilot** you do nothing: the operator merges each merge request
once CI is green and the review is clean, and closes the issue with
`agent::done`.

**Without autopilot**, merge the merge request yourself. Its `Closes #42`
closes the issue when it lands on the default branch; you can also close
the issue or set `agent::done` yourself.

On the next poll the issue's task, its pod and its workspace are deleted.
If you close an issue without merging, the same happens, and the merge
request stays open for you to close.

## Next

- [Answer and hand over](answer-and-hand-over.md): answer the agent's
  questions.
- [Write a milestone](write-a-milestone.md): plan the next one.
- [Commands](../reference/commands.md): every command.

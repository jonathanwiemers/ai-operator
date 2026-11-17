# Guides

Each guide shows how to do one task once ai-operator runs: write a milestone,
answer the agent, run a milestone on autopilot, handle a pause, steer from
the Desk, watch spend, release. Guides show the Desk first and the `/agent`
comment second.

## Pages

In the order you need them for a milestone:

1. [Write a milestone](write-a-milestone.md): the spec, its marker lines,
   what the planner makes of it, and the order of the issues.
2. [Answer and hand over](answer-and-hand-over.md): answer the agent's
   questions in the Desk or in GitLab, and hand an issue over by label.
3. [While the agent works](while-the-agent-works.md): the draft merge
   request, its steps and review, commit messages, toolchains, CI, the
   default branch's health and merge conflicts.
4. [Owners and pings](owners-and-pings.md): who gets mentioned when the
   agent needs a human, and who may give commands.
5. [Iterate and finish](iterate-and-finish.md): ask for changes, revisit
   decisions, also after the merge, and finish an issue.
6. [Run a milestone on autopilot](autopilot.md): from plan to last merge,
   several issues and milestones at once, the merge gate and the tracking
   issue.
7. [When autopilot pauses](pauses.md): pauses and holds, how they end, and
   each kind with its ways out.
8. [The pilot](pilot.md): ask it, let it act within limits, change the
   spec through it, and the retrospective.
9. [When runs go wrong](when-runs-go-wrong.md): cut-off runs, usage limits,
   a slow GitLab, and issues that wait.
10. [The Desk](desk.md): log in, who sees what, and what each view shows.
11. [Steer from the Desk](steer-from-the-desk.md): chat with an agent, ask
    the pilot, answer prompts, revisit decisions, pause cards, the queue,
    the planner, costs and the Desk on a phone.

The other guides are being written from the old pages, see the
[docs map](../README.md#pages-being-moved).

## Next

- [Commands](../reference/commands.md): every `/agent` command.
- [How it works](../concepts/how-it-works.md): what happens between spec
  and merge.

# Guides

Each guide shows how to do one task once ai-operator runs: write a milestone,
answer the agent, run a milestone on autopilot, handle a pause, steer from
the Desk, watch spend, try cost levers, release. Guides show the Desk first and the `/agent`
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
12. [Order milestones and read the timeline](milestone-order-and-timeline.md):
    drag milestones into run order, `/agent move`, and the issue and
    Overview timelines.
13. [Read and revisit decisions](decisions.md): the Decisions tabs, search,
    the card, the chain of revisits and what Revisit does.
14. [Budgets and spend](budgets-and-spend.md): what counts as spend, budgets
    for a milestone and its issues, and the Metrics tab and Metrics page.
15. [Cost levers](cost-levers.md): try fresh step sessions, a lower effort
    and a cheaper profile side by side in an experiment, and read the arm
    table and the Context panel.
16. [Releases](releases.md): release points in the plan, `/agent release`,
    the final release, the sweep and the version format.
17. [Cross-repo changes](cross-repo-changes.md): a change that spans a
    companion repository, and how its merge request lands.
18. [Watch from Kubernetes](watch-from-kubernetes.md): `kubectl` views of
    projects and tasks, run logs, and what happens when an agent container
    or a pod is lost.
19. [Research](research.md): news flashes and one-off reports, turning
    research on, choosing agents, retention and troubleshooting.
20. [Project sets](project-sets.md): one AgentProjectSet for a whole GitLab
    group, active and dormant projects, globs and the request budget.
21. [Upgrade](upgrade.md): move to a new release, and what each release
    removed from manifests and Helm values.

## Next

- [Commands](../reference/commands.md): every `/agent` command.
- [How it works](../concepts/how-it-works.md): what happens between spec
  and merge.

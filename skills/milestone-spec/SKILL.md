---
name: milestone-spec
description: Write or review a GitLab milestone spec for ai-operator, the description the planner turns into issues and the autopilot builds end to end. Use when someone asks to plan, draft, create or review a milestone for a project an AgentProject watches, or says "write a milestone", "new autopilot milestone" or "spec this for the operator".
---

# Writing a milestone for ai-operator

A milestone description is the whole spec of one feature. The planner reads
it once, when the milestone's turn comes, and splits it into one issue per
merge request. On autopilot nobody answers questions after that: every gap
in the spec becomes a default the agents pick on their own. So the spec has
to say what you want, what you already decided, and what is out of scope.

This skill is the house style. [`template.md`](template.md) is the skeleton
to copy. The rules for the marker lines are in
[`docs/commands.md`](../../docs/commands.md#in-the-milestone-description),
and what the planner and the autopilot do with a spec is in
[`docs/usage.md`](../../docs/usage.md#1-write-the-milestone-spec). When
those docs and this skill disagree, the docs win.

## Before you write

1. **Read the code first.** The Background section names files and
   functions as they are today. Fetch the default branch and note its short
   SHA and the date.
2. **Check the queue.** Autopilot milestones of a project run one after
   another, oldest first, and each is planned only when its turn comes. List
   the open milestones and say in Background which ones run before this one
   and what they change that this one builds on.
3. **One capability per milestone.** A user should be able to name what
   changed in one sentence. If the goal needs "and also" twice, write two
   milestones.
4. **Settle the decisions with the owner.** Anything you would ask about
   later goes into Decisions now. Autopilot cannot ask.

## Marker lines

The first lines of the description, each ending with two spaces so GitLab
shows them as separate lines:

```markdown
/agent spec  
/agent autopilot  
/agent owners @gitlab-user  
/agent owner-subjects desk-login  
```

- `/agent spec` plans the milestone. `/agent autopilot` also runs it to the
  end. Leave out `autopilot` if a human should hand over each issue.
- `/agent owners` are the GitLab users who get pinged.
  `/agent owner-subjects` are the Desk logins that may answer prompts. There
  is no project-level fallback for the second one, so always set it.
- Optional lines, only when they apply:
  - `/agent repo <group/project> [merge]` when issues change a companion
    repository. With `merge`, autopilot may merge it there too.
  - `/agent budget <usd>` caps what the milestone may spend.
  - `/agent followup-of <milestone> <n…>` when this milestone takes over
    follow-ups from an earlier retrospective.

## Title

The outcome a user notices, in plain words. Prefix the area when the
project has several, like `Desk:`. No ticket numbers, no implementation.

- Good: `Desk: stay logged in while you use it (OIDC refresh tokens)`
- Good: `Runs that hang get noticed and restarted`
- Bad: `Refactor session handling in internal/ui`

The `# heading` under the marker lines repeats the title.

## Sections

Use these headings, in this order. Most good specs land at 150 to 220 lines.

**Goal.** Numbered outcomes, each one observable by a user or an operator.
Three to five is typical. These are what Acceptance checks.

**Background.** What exists today, with file and line refs, and the first
line says what they are pinned to: `File refs are at origin/main <sha>,
<date>.` Then name the milestones that run before this one. Add real
numbers when they matter (sizes, timings, counts from recent runs). The
planner checks the code again when it plans, so refs only need to be right
on the day you write them.

**Decisions (locked).** Numbered. Each one says what was decided, why, and
which alternatives were rejected. Requirements point at them ("Decision 3").
This section stops the planner and the implementers from reopening
questions you already answered.

**Requirements.** Numbered `R1`, `R2` and so on, grouped under `###`
headings by area (for example `Operator (Go)`, `UI server (Go)`, `Web`,
`Infra (companion)`, `Docs`). Each requirement says what changes and how it
is tested (`Tests: …`). Keep one requirement to about one merge request.
Always end with a `### Docs` group that names the pages that change and the
spec file `docs/specs/<name>.spec.md` with a steering log.

**Constraints.** The rules every merge request follows:
- the repository's size budget per MR (ai-operator: under 50 production
  lines, Go and web in separate MRs)
- what must not change (defaults, existing tests, prompts of other roles)
- no new dependencies, unless a Decision says otherwise
- the checks to run (`make test`, `go vet ./...`, `gofmt`, `make helm-lint`,
  `make ui`, `make ts-types`)
- when a requirement has to be narrowed: open a follow-up issue and log it
  in the spec's steering log

**Non-goals.** What a reader might expect but this milestone does not do.
Name the tempting next step explicitly.

**Suggested order.** One line, for example
`R1 → R2 → R3, R4 → R5 → R6`. Commas mean the requirements are independent.

**Acceptance.** Scenarios you can check by hand once it is merged, in the
form "On a milestone with …, … happens". Each Goal has at least one.

## Writing style

- Short sentences and plain words. Write what happens, not what is "handled"
  or "supported".
- Be concrete: names of fields, commands, labels, files, numbers.
- No "TBD", "maybe" or open questions. Decide, or move it to Non-goals.
- Do not dictate code the planner can work out. Do say where behaviour must
  stay the same.

## Checklist

Go through this before you create the milestone:

- [ ] Marker lines first, with owners and owner subjects
- [ ] Title is a user-visible outcome
- [ ] Background is pinned to a SHA and a date, and names the milestones
      that run before this one
- [ ] Every question you would ask later is a Decision
- [ ] Every requirement has a test line and fits about one MR
- [ ] A Docs group with the pages and the spec file
- [ ] Companion repository lines and an Infra group if another repo changes
- [ ] Non-goals name the tempting next step
- [ ] Every Goal has an Acceptance scenario

## Creating it

Create the milestone in GitLab and paste the description. On autopilot it
waits for its turn and the tracking issue says so. Edits before planning
start nothing, so you can keep refining until it is planned. Once it is
planned, editing the description re-plans the issues that are still drafts.

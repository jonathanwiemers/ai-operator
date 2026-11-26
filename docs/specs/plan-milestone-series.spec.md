# Desk plans: draft a series of milestones that build on the queue

Status: implemented. Deviations from the requirements go into the
[Steering log](#steering-log). The behaviour is documented in
[Write a milestone](../guides/write-a-milestone.md#draft-a-series-of-milestones),
the [Desk API](../reference/desk-api.md#plan-drafts) and
[Desk plans](../contributing/design.md#desk-plans).

## Goal

1. A plan agent knows what the queued and running milestones of its project will build: their goals, locked decisions, requirements, planned issues and what has merged. It writes its draft against the code as it will be when the new milestone's turn comes, does not respecify or contradict queued work, and its Background has a "Builds on" part that names each thing it uses from an earlier milestone and whether that is on main yet.
2. When the owner writes `%38` in a plan message, the agent reads milestone %38 in full before it answers.
3. Typing `%` in the plan chat opens a menu of the project's milestones (number, title, state) and of the plan's own drafts. Picking one inserts its reference.
4. One plan can hold up to 5 drafts, in order. The owner sees one tab per draft and can edit, remove and reorder them. A later draft can build on an earlier one.
5. The owner creates the drafts' milestones one by one or with **Create all**, always in list order, so the milestone IIDs follow the order the drafts build on each other. A `%draft-N` reference in a draft becomes the real `%IID` when that milestone is created.

## Background

Before this milestone a plan (`AgentPlan`) held one draft in `status.draft` and one `createdMilestoneIID`, and ended once its milestone was created. The plan prompt sent the open milestones with only their first 3 description lines, the titles of the 5 newest closed ones and 50 open issues, all under 16 KiB: no autopilot state, queue position, requirements, decisions, planned issues or merges. Milestones run oldest first (strict milestone order), and a run-next mark (`/agent start`) moves one ahead, so a later milestone is planned against code its predecessors have already merged.

## Decisions (locked)

1. **Milestone files, not a longer prompt.** On every turn, after it syncs the checkout, the runner writes one markdown file per milestone to `.plan-context/milestones/<iid>.md`, for every open milestone and the 5 newest closed ones. The prompt carries only an index. Rejected: pasting full specs into the prompt, and giving the agent a GitLab tool.
2. **The folder never leaves the pod.** `.plan-context/` is in the checkout's `.git/info/exclude`, deleted and written again on each turn. Each file opens with a line saying its text is untrusted data from GitLab.
3. **What a milestone file holds:** the title and the description without operator lines; the autopilot state; the queue position; the issues (one list request, state all), each with its state, its merge request and merge SHA when merged, and its description cut at 4 KiB. One file is at most 64 KiB, cut at an issue boundary.
4. **Queue position** is the order in which the milestones will run: the running or planned milestone first, then a milestone with a run-next mark, then the waiting ones by lowest IID. One function computes it for the index and the files.
5. **References in a message.** Every `%N` in the owner's message gets one prompt line pointing at its file. A milestone without a file is fetched and written too, at most 5 per turn, through a new Get method of `planGitLab`.
6. **How the agent builds on queued work.** New rules in the plan system prompt: read the files of earlier milestones in the same area before the first draft; write Background against the future code with a "Builds on" part; reuse their names, fields and decisions and name their work in Non-goals; say so on overlap and propose merging or narrowing; the same between the plan's own drafts.
7. **Several drafts per plan.** `status.drafts` is an ordered list of at most 5 `PlanDraft`s (`key`, `title`, `draft`, `revision`, `by`, `sha`, `createdMilestoneIID`, `createdMilestoneURL`). A key is never reused within the plan. The agent's answer returns `drafts: [{key, title, draft}]` with only the drafts it changed; key 0 adds one. Only the owner removes and reorders drafts. Rejected: one plan starting sibling plans.
8. **Old plans keep working.** `DraftList()` returns the old single draft as key 1; the first write moves it and clears the old fields, which stay in the CRD as deprecated. `CreatedMilestoneIID` keeps the first created IID.
9. **The plan-decisions milestone's decisions stay per plan**, not per draft.
10. **Strict create order.** Only the first uncreated draft can be created (409 otherwise). `%draft-N` of a created draft becomes `%IID`; of an uncreated one it is refused with 422. A created draft is read-only. The phase becomes `Created` only when every draft has its milestone.
11. **Create all** is a web loop over the same endpoint, in order, with the same choices for every draft, stopping at the first error. No batch endpoint.
12. **The `%` menu** uses `GET /api/v1/milestones?project=…&state=all` and the plan view: open milestones in queue order, then the newest closed ones, then the uncreated drafts as `%draft-N`. Typing filters by number or title; arrows, Enter and Escape. Only in the plan chat.

## Non-goals

- The planner checking a milestone's "Builds on" assumptions against what merged when its turn comes.
- Reordering, editing or closing existing milestones from a plan.
- A dependency marker between milestones.
- The `%` menu in the draft editor, task chats or the stream composer.
- Splitting decisions per draft.
- Reading merge request diffs or recorded decisions into the milestone files.
- More than 5 drafts per plan.

## Requirements and where they were implemented

- **R1** Milestone files, the queue position and `.git/info/exclude` (runner).
- **R2** The milestone index, `%N` references and the extra files (runner, GitLab client).
- **R3** The "Builds on" rules in the plan system prompt.
- **R4** `PlanDraft`, `status.drafts`, `DraftList()` and `Active()` (API).
- **R5** Several drafts per turn, draft files and per-draft owner edits (runner).
- **R6** Drafts in the plan view, edit by key, remove and reorder (UI server).
- **R7** Create in strict order with `%draft-N` replaced (UI server).
- **R8** One tab per draft and **Create all** (web).
- **R9** The `%` menu in the plan chat (web).
- **R10** These docs and this spec.

## Steering log

- **`GetMilestoneByIID` instead of `GetMilestone`** (Decision 5, R2). The client's `GetMilestone` already existed and takes the milestone's global ID, as GitLab's single-milestone endpoint does, while `%N` is the project IID. The new method lists the project's milestones with `iids[]` and answers 404 when none matches, so `planGitLab` still has only Get and List methods. Fetches that find nothing count toward the 5 per turn.
- **`RunOrder` lives in `internal/milestoneorder`** (Decision 4), not in `internal/runner`, next to the `Rank` and `Ahead` the controller and the Desk queue use, so the three cannot disagree. A started milestone (it has items and is not done) stays ahead of a newer run-next mark. The Desk's milestone rows got the same order as `queuePosition`, which the `%` menu sorts by.
- The 5 newest closed milestones are taken from the first page (100) of closed milestones, by IID. A milestone description is cut at 32 KiB inside its file, so its issues keep room in the 64 KiB.
- Draft files (`.plan-context/drafts/<key>.md`) open with a line naming the draft and its state instead of the untrusted line: they hold the plan's own text, not GitLab's.
- The owner cannot add a draft through the API; only the agent adds drafts (Decision 7). `PUT …/draft` and `POST …/milestone` without a `key` act on the first draft without a milestone, so an old client keeps working on a plan's first draft.
- R6 landed before R5, so the UI server's edit by key was in place before the runner wrote several drafts.
- No requirement was narrowed, and no follow-up issue was opened for this milestone.
- **Later: references to later drafts are accepted** (Decision 10). A later milestone changed the 422 for a `%draft-N` of a later uncreated draft: the description now gets that draft's quoted title, filled in with its `%IID` when it is created. See [plan-forward-refs](plan-forward-refs.spec.md).

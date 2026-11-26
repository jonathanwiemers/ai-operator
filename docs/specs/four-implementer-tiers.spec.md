# Four implementer tiers, and an explicit model and effort for every agent

Status: the milestone's spec, run by autopilot. Narrowed requirements and
in-flight changes go into the [Steering log](#steering-log). The user pages
are [Agents and models](../reference/agents-and-models.md#profiles) and
[The pilot](../guides/pilot.md#raise-an-issues-tier).

## Goal

1. Every agent call runs on an explicit model **and** effort. No role depends on the Claude Code CLI's own effort default any more.
2. Implementers run on one of four tiers, the default profiles:

   | Tier | Model | Effort | For |
   |---|---|---|---|
   | `fast` | `claude-sonnet-5-5` | `medium` | simple work |
   | `balanced` | `claude-sonnet-5-5` | `high` | more complex work |
   | `strong` | `claude-opus-5-5` | `medium` | rather complex work |
   | `deep` | `claude-opus-5-5` | `high` | very complex work |

3. The planner picks **one tier per issue** by a written rubric. It picks the lowest tier that fits, and goes one up when in doubt. It writes a one-line reason. The owner sees the tier, its model and effort and the reason on the issue and in the Desk.
4. The pilot can propose a higher tier for an issue; the owner accepts it.
5. The ai-operator and Bloop projects run on the four tiers, and the `cost-levers` experiment ends.

## Background

Before this milestone:

- **No role had a default effort.** The runner passed `--effort` only when the role, profile or label set one. Role defaults set only engine and model: the planner and implementer on Opus, the reviewer, describer and pilot on Sonnet.
- **Roles that reuse other roles.** The Desk plan agent and the spec chat run on `agents.spec`. The pilot's retrospective runs on `agents.implement`. The conflict role takes the implementer's effort unless it sets its own.
- **Default profiles** were `fast` (Sonnet) and `strong` (Opus), without effort. A project that lists profiles replaces them.
- **The planner picked two things separately:** a profile from the profiles' names and descriptions, and an optional effort. Both became `agent-profile::` and `agent-effort::` labels, on create and on re-plan. A re-plan kept any profile or effort label already there, so it never changed a draft issue's tier. The labels the plan set were recorded in the description's `plan-labels` marker, so the cost-levers experiment could tell them from human labels. The planner gave no reason for either pick.
- **Effort resolution.** `IssueRole` applies the profile, then a model label, then an effort label capped at `agents.maxEffort` (default `high`). A role's own effort is not capped.
- **The Desk** showed no tier.
- **The pilot** acts only through proposals the owner accepts. None of its tools changed an issue's profile.
- **In practice nearly every planned issue was `agent-profile::strong`** (Opus) with effort `medium` or `high`; the Sonnet profile was hardly used.
- **Infra.** The infra AgentProjects defined their own profiles (`fast` on Cursor auto, `balanced` on Sonnet, `strong` on Opus) and ran the `cost-levers` experiment.

## Decisions (locked)

1. **D1 Tiers are profiles.** The four tiers are the default profiles, each with a fixed model and effort. The planner picks only the tier and no longer picks an effort. Human labels and `/agent use` and `/agent effort` still override, as do experiment arms; `IssueRole` keeps that precedence. `agents.maxEffort` still caps labels only.
2. **D2 Names and rubric.** The tiers are named `fast`, `balanced`, `strong` and `deep`. The rubric lives in the default profiles' descriptions, configurable per project. A fixed planner rule applies on top: pick the lowest tier whose description fits, one up when in doubt, and give a one-line reason. The reason shows on the issue and in the Desk.
3. **D3 Explicit defaults for the other roles.** These apply when a role sets no effort and runs on claude: planner and Desk plan agent (`agents.spec`) Opus, `high`; reviewer Sonnet, `medium`; pilot Sonnet, `medium`; describer Sonnet, `low`; the conflict role follows the implementer, unchanged. A role on cursor gets no effort.
4. **D4 No automatic escalation.** An issue changes tier only by a label: a human sets one, or an owner accepts the pilot's proposal. Automatic escalation after failures is a follow-up issue, once the tiers have data.
5. **D5 Infra.** One infra companion MR, merged by the owner and not by the autopilot: it replaces the profiles of the Bloop and ai-operator AgentProjects with the four tiers, keeps Cursor only as the fallback of `fast` and `balanced`, and removes the `cost-levers` experiment.
6. **D6 The pilot raises a tier with a `setTier` proposal.** Nothing happens until an owner accepts it. On the owner's Accept, the operator swaps the issue's profile label and drops its model and effort labels; the next run uses the new tier. The pilot proposes one tier up only after two failures on the same problem, or after review rounds that ran out on substance.
7. **D7 Issues without a profile label run on Opus·medium.** `agents.implement` gets effort `medium` by default, the same as `strong`. This covers hand-made issues and loose issues.

## Requirements

- **R1. Explicit role defaults (D3, D7).** The role defaults fill in an effort for claude roles that set none: `spec` `high`, `review` `medium`, `pilot` `medium`, `describe` `low`, `implement` `medium`. A role's own `effort` wins, a cursor role gets no effort, the fallback's effort stays its own, and the conflict role is unchanged. Done when unit tests show every role's resolved model and effort, and a runner test shows the `--effort` flag on a reviewer call of a project that sets nothing.
- **R2. Four default tiers.** `DefaultProfiles` returns `fast`, `balanced`, `strong` and `deep` with the models and efforts of the Goal table, in that order. The descriptions carry the rubric:
  - `fast`: follows an existing pattern, touches a few files, adds no new API, type or migration, leaves no decision open (docs, config, tests of existing code, small UI changes).
  - `balanced`: new logic in one package or one area of the UI, with a clear spec and some edge cases.
  - `strong`: crosses layers (for example API, controller and Desk), changes a CRD or the store schema, or needs design within the issue.
  - `deep`: concurrency, reconcile or state-machine logic, security, spend accounting, stream parsing, changes that can lose data, or work whose earlier attempt failed.
- **R3. The planner picks one tier and gives a reason.** The plan schema loses `effort` and gains a required `profileWhy` (one line, at most 200 characters). The prompt rule is: pick the lowest tier whose description fits; when in doubt, one up; give the reason, naming the rubric point that decided it. The profile list in the prompt shows each profile's engine, model and effort. `effortLabels` goes, so the `plan-labels` marker holds only the profile label. `renderIssue` writes a visible line near the end of the description: `Tier: **<name>** (<model>, effort <effort>): <profileWhy>`. `PlannedIssue` gains `Profile` and `ProfileWhy`. On a re-plan of a **draft** issue, a profile or effort label equal to the one in its `plan-labels` marker is the plan's own: the planner replaces that profile label with its new pick and removes that effort label. Any other profile, effort or model label stays.
- **R4. The Desk shows the tier.** `MilestoneIssueRow` gains `tier` with the profile name, model, effort and reason, taken from the snapshot's planned issue and the project's profiles. The milestone Issues table shows it as a badge, for example `balanced · Sonnet · high`, with the reason in its tooltip. The issue page shows the tier and the reason in its header meta. It uses Nuxt UI components.
- **R5. The pilot proposes a tier (D6).** A new pilot tool `setTier` with `issue` and `profile`. `checkAction` rejects an unknown profile and an issue outside the milestone. When an owner accepts it, the operator sets `agent-profile::<profile>` on the issue, removes its other profile labels and its `agent-model::` and `agent-effort::` labels, and says so in the pilot's acted entry. The next run uses the tier; a running call is not restarted. The pilot's prompt says when to propose it. `setTier` needs an Accept: it is not one of the chat tools that run directly.
- **R6. Infra companion MR** in the infra repository, opened after the release that ships R1 to R3, and merged by the owner: the profiles become the four tiers, `fast` and `balanced` get `fallback: {engine: cursor, model: auto}`, `agents.implement.experiment` is removed, and `agents.spec` is Opus at `high`, unless already set.
- **R7. Docs.** `docs/reference/agents-and-models.md` names the explicit defaults and shows the four tiers with a complete example; "Per-issue model and effort" no longer says the planner sets effort labels; `arms[].effort` replaces the profile's effort. Every page that says the planner picks an effort is updated, the glossary gets a "tier" entry, `setTier` is documented where the pilot's tools are, and this spec is archived and listed.

## Constraints

- Under 300 production lines per merge request; tests, generated files and docs do not count.
- Keep `IssueRole`'s precedence: profile, then model label, then effort label capped at `maxEffort`. Experiment arms still override the planner's profile; labels set by humans or commands still win over arms.
- Existing issues keep working: `agent-profile::fast` and `agent-profile::strong` stay valid names, and issues that are no longer drafts keep all their labels, the planner's effort labels included.
- `maxEffort`'s default stays `high`, so `deep` stays within it. A role's or profile's own effort stays uncapped.
- Research keeps choosing its agent as before.
- The pilot's `instruct` path does not change.

## Non-goals

- Automatic escalation of an issue's tier after failures (D4): a follow-up issue.
- A `/agent tier` command: humans change the `agent-profile::` label by hand, or use `/agent use` and `/agent effort`.
- Showing tiers for finished milestones whose tasks are gone.
- Showing in the Desk when a label or an arm overrode the planner's tier.
- Removing the experiment feature from the code: only the infra experiment ends.
- Changing models or efforts of research runs, or the fallback mechanics.

## Acceptance

- A project without `agents` settings runs every claude call with an explicit effort.
- An issue without a profile label runs on Opus at effort `medium`.
- A newly planned milestone's issues each carry exactly one `agent-profile::` label from `fast`, `balanced`, `strong` and `deep`, and no `agent-effort::` label. Each issue has a tier line with a reason in its description.
- The Desk's Issues table and issue page show each planned issue's tier, model, effort and reason.
- Re-planning a draft issue moves it to the planner's new tier, unless a human labelled it.
- An accepted pilot `setTier` proposal changes the issue's tier for its next run.
- The infra companion MR is open with the four tiers, Cursor as the fallback of the two Sonnet tiers, and no experiment, waiting for the owner.
- A follow-up issue for automatic escalation exists.

## Steering log

- #870 (R7): the pilot docs also say that `setTier` is allowed on the loose issues queue. Older archived specs keep their wording about planner effort labels (D870-P2).

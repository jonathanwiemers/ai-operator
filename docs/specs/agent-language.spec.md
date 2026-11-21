# Agents write in English, set by one chart value

Status: implemented. Deviations from the requirements go into the
[Steering log](#steering-log).

## Goal

1. Every agent writes in one configured language, whatever language the owner or the issue uses. This covers the implementer, planner, reviewers, describer, pilot, Desk chats, Desk plans and research.
2. The language is one Helm value, `agent.language`, for the whole operator. Its default is `en`, so a fresh install and an upgrade both write English.
3. An operator can choose another language (`agent.language: de`) or turn the rule off (`agent.language: ""`). With the rule off, the agents copy the language they read.
4. An invalid value stops `helm install` and `helm upgrade`, and the operator refuses to start with it.

## Decisions (locked)

1. **One operator-wide setting, from the chart**, passed as `--agent-language`. No `spec.agents.language`. Rejected: a per-project field, and per-role `instructions`.
2. **Default `en`, in the chart and in the binary**, so an operator started without the chart behaves the same.
3. **A BCP 47 language tag** (`en`, `de`, `pt-BR`). The prompt names the language in English through `golang.org/x/text/language/display`. Rejected: free text and a fixed list.
4. **Empty turns the rule off.** No rule and no `AI_AGENT_LANGUAGE`.
5. **The rule is added in one place, the agent server.** It appends the rule to the end of the system prompt of every one-shot call and session, for every engine. The language reaches the agent container as `AI_AGENT_LANGUAGE`, set for task, plan and research pods. The rule goes after the project's `instructions`, so the chart setting wins.
6. **The rule text is fixed**; only the language name changes.
7. **The pilot no longer mirrors the message's language.**
8. **A pod picks up a change when it next starts.** Nothing restarts running pods.
9. **Validation in two places.** Helm rejects a tag that does not match the tag pattern; the operator rejects one that `language.Parse` refuses.

## Non-goals

- A per-project or per-role language.
- Translating the operator's own GitLab comments, labels, Desk texts or docs.
- Restarting running pods when the value changes.
- Detecting or rewriting agent output that is in another language.

## Requirements and where they were implemented

- **R1** Chart value, flag, validation and `AI_AGENT_LANGUAGE` in the pod environment.
- **R2** The language rule in the agent server; the pilot prompt change.
- **R3** Docs: the chart README, [Agents and models](../reference/agents-and-models.md#language), [Roles](../concepts/roles.md) and this spec.

## Steering log

- R1 and R2 shipped in one merge request.
- The Helm checks (default renders `--agent-language=en`, empty renders `--agent-language=`, an invalid tag fails and names `agent.language`) live in `make helm-lint`.
- No requirement was narrowed and no follow-up issue was opened.

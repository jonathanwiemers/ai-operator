# Reference

Look things up here while you work: every `/agent` command, the fields of
an `AgentProject`, the labels and the Desk API. Default values are stated
here once, and the guides link to them. The Helm chart values are in the
[chart README](../../charts/ai-operator/README.md).

## Pages

1. **[Commands](commands.md)**: every `/agent` and `/pilot` command, grouped
   by where you write it, and the marker lines of a milestone description.
2. **[Labels](labels.md)**: the `agent::` state labels and what moves an
   issue between them, the labels you may set, and the ones the operator
   sets.
3. **[AgentProject](agentproject.md)**: a complete example, then the GitLab,
   milestone, autopilot, house rules, workflow and release, notify, pilot,
   budget, webhook and cross-repo fields, each with its default.
4. **[Agents and models](agents-and-models.md)**: `spec.agents`, the roles,
   subagents, the read gate, model and effort per issue, profiles,
   fallbacks and engines.
5. **[Lifecycle and CI](lifecycle-and-ci.md)**: pods and workspaces,
   retries, CI feedback, checkpoints and the default branch's health.
6. **[Agent image](agent-image.md)**: what the agents run in, how to extend
   it, and the network policy.
7. **[Desk API](desk-api.md)**: every endpoint of the Desk with what it is
   for and the grant it needs.

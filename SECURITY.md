# Security policy

ai-operator runs coding agents with credentials (a GitLab token, agent CLI
credentials) on content that anyone with access to your GitLab project can
write. Security issues are taken seriously.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through
[GitHub security advisories](https://github.com/jonathanwiemers/ai-operator/security/advisories/new)
("Report a vulnerability" on the Security tab).

Include what you found, how to reproduce it and which version you used. You
will get an answer within a week.

## Supported versions

Only the latest release (`YEAR.MONTH.N`) gets fixes. Upgrade with
`helm upgrade` to the newest version.

## Scope

The threat model is described in [docs/design.md](docs/design.md#security).
In particular, these are in scope:

- the model, or instructions planted in issues, comments, code or CI logs,
  getting hold of the GitLab token or Kubernetes credentials
- code from the agent's checkout being executed in the runner container or
  by the operator
- the operator or the agent pods being granted more Kubernetes permissions
  than documented

Known and accepted (see "Remaining risks" in the design doc): the agent can
push arbitrary content to its own `agent/*` branch and write arbitrary text
into its GitLab comments. Humans review the draft MR before merging.

# Documentation

ai-operator turns GitLab milestones and issues into work for coding agents in
Kubernetes. The docs are in four sections for users, Getting started,
Guides, Reference and Concepts, plus a section for contributors and the
specs behind each feature. New here? Start with Getting started.

![Desk overview with running, paused and done milestones](assets/screenshots/dashboard.webp)

The docs are being reorganised into these sections. Until a page has moved,
it is listed under [Pages being moved](#pages-being-moved).

## Getting started

**[getting-started/](getting-started/README.md)**: install the operator,
turn on the Desk and get your first milestone planned.

## Guides

**[guides/](guides/README.md)**: one task per page, from writing a
milestone to running it on autopilot and steering it from the Desk.

## Reference

**[reference/](reference/README.md)**: every `/agent` command, the
`AgentProject` fields, the labels and the Desk API, for looking things up.

## Concepts

**[concepts/](concepts/README.md)**: how it works, the roles of the agents,
security and a glossary.

## Contributing

**[contributing/](contributing/README.md)**: the design, web UI
development, releasing and the sandbox test plan, for people who work on
ai-operator itself.

## Specs

**[specs/](specs/README.md)**: the specs behind each feature, with the
problem, the decisions and how it was split into merge requests. Read one
to learn why something works the way it does.

## Pages being moved

The old pages, until their sections have moved to the sections above. The
[docs revamp spec](specs/docs-revamp.spec.md#inventory) lists where each
section goes.

- **[Usage](usage.md)**: the GitLab workflow. Labels, milestone specs,
  questions and decisions, CI, autopilot and its pauses, the pilot, owners,
  budgets and the retrospective.
- **[Commands](commands.md)**: every `/agent` command and the labels behind
  them.
- **[Configuration](configuration.md)**: installation with Helm or Flux,
  secrets, the `AgentProject` reference, engines, profiles, models and the
  agent image. Chart values are in the
  [Helm chart README](../charts/ai-operator/README.md).
- **[Web UI](ui.md)**: the Desk. What it shows
  ([screenshots](ui.md#screenshots)), OIDC login, roles, steering, the API
  and how to develop and check it.
- **[Research](research.md)**: news flashes, scheduled and one-off
  researches next to the projects, with their own database.

## Assets

**[assets/](assets/README.md)**: the logo, the architecture diagram and the
Desk screenshots used in these pages.

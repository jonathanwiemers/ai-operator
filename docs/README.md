# Documentation

ai-operator turns GitLab milestones and issues into work for coding agents in
Kubernetes. The docs are in four sections for users, Getting started,
Guides, Reference and Concepts, plus a section for contributors and the
specs behind each feature. New here? Start with Getting started.

![Desk overview with running, paused and done milestones](assets/screenshots/dashboard.webp)

The docs are being reorganised into these sections. Until a page has moved,
it is listed under [Pages being moved](#pages-being-moved).

## Getting started

**[getting-started/](getting-started/README.md)**: [install](getting-started/install.md)
the operator, [turn on the Desk](getting-started/turn-on-the-desk.md) and
get [your first milestone](getting-started/first-milestone.md) planned.

## Guides

**[guides/](guides/README.md)**: one task per page.
[Write a milestone](guides/write-a-milestone.md),
[answer and hand over](guides/answer-and-hand-over.md), follow it
[while the agent works](guides/while-the-agent-works.md), name the
[owners](guides/owners-and-pings.md),
[iterate and finish](guides/iterate-and-finish.md), run it
[on autopilot](guides/autopilot.md), handle
[pauses](guides/pauses.md), work with [the pilot](guides/pilot.md), and
know what happens [when runs go wrong](guides/when-runs-go-wrong.md).

## Reference

**[reference/](reference/README.md)**: every `/agent` command in
[commands](reference/commands.md), the [labels](reference/labels.md), the
[`AgentProject` fields](reference/agentproject.md) and the
[Desk API](reference/desk-api.md), for looking things up.

## Concepts

**[concepts/](concepts/README.md)**: [how it works](concepts/how-it-works.md),
the [roles](concepts/roles.md) of the agents,
[security](concepts/security.md) and a [glossary](concepts/glossary.md).

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

- **[Usage](usage.md)**: budgets, companion repositories, releases,
  prompts, and watching progress from the Desk and from Kubernetes.
- **[Web UI](ui.md)**: the Desk. What it shows
  ([screenshots](ui.md#screenshots)), roles and steering.
- **[Research](research.md)**: news flashes, scheduled and one-off
  researches next to the projects, with their own database.

## Assets

**[assets/](assets/README.md)**: the logo, the architecture diagram and the
Desk screenshots used in these pages.

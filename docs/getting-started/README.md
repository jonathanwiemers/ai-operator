# Getting started

Start here if you are new to ai-operator. These pages take you from an empty
cluster to a first milestone: you install the operator, connect it to a
GitLab project, turn on the Desk, write a short milestone spec and watch the
agent plan it. Read them in order. You do not need the contributor docs for
any of it.

## Pages

1. **[Install](install.md)**: what you need, the GitLab setup (bot user,
   labels, board, webhook), the operator with Helm or Flux, the Secrets, your
   first `AgentProject` and the database.
2. **[Turn on the Desk](turn-on-the-desk.md)**: the OIDC client, the
   Secret and the chart values, then log in.
3. **[Your first milestone](first-milestone.md)**: write a short spec on
   autopilot, watch it get planned in the Desk, see the first merge request
   and answer a prompt.

## Next

- [How it works](../concepts/how-it-works.md): the big picture.
- [Reference](../reference/README.md): commands, labels and every
  `AgentProject` field.

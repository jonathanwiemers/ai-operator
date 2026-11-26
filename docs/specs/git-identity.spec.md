# Agent commits carry the bot's GitLab identity

Status: the milestone's spec, run by autopilot. Narrowed requirements and
in-flight changes go into the [Steering log](#steering-log). The user page is
[Security](../concepts/security.md#commit-identity); the status fields are in
[Status and history events](../reference/status-and-history.md#botgitname-and-botgitemail).
Where this spec and the docs disagree, the docs win.

## Goal

1. Every commit the operator makes (start commit, step commits, checkpoint
   commits, companion repo commits) is authored and committed by the GitLab
   user that owns the project's token, with that user's name and an email
   GitLab links to the user's profile.
2. Nobody has to configure the identity: it comes from the token.
3. `ai-operator@noreply.local` is gone from the commits of a running cluster.

## Background

Commits pushed by the operator showed `AI Operator <ai-operator@noreply.local>`.
GitLab cannot link that address to any user, so the commits had no avatar and
no profile, and were not attributed to the bot account. The runner read
`AI_GIT_AUTHOR_NAME` and `AI_GIT_AUTHOR_EMAIL` with that default, in two
places and a third copy in `StartBranch`, and the controller never set them.
The controller already resolved the token user on every poll
(`status.botUserID`, `status.botUsername`). The agent container never holds a
GitLab token, so it cannot ask GitLab itself.

## Decisions

1. **Source: the token user**, from `GET /user`, which the controller already
   calls. No new spec field, and no second call from the runner.
2. **Name:** the user's `name`; the username when `name` is empty.
3. **Email:** the user's `commit_email`; otherwise GitLab's private commit
   address `<id>-<username>@users.noreply.gitlab.com`. Never the private
   `email` field.
4. **Resolved in the controller** on every poll and kept in
   `status.botGitName` and `status.botGitEmail`.
5. **Delivered as env** (`AI_GIT_AUTHOR_NAME`, `AI_GIT_AUTHOR_EMAIL`) on the
   runner and the agent container. They are not secret; the agent container
   still gets no token.
6. **Companions use the home identity**, also when the companion token is
   another user.
7. **One fallback:** when the status has no identity yet, pods get no env and
   the runner keeps its default in one place.

## Requirements

- **R1** `gitlab.User` decodes `name` and `commit_email`.
- **R2** The poll stores the name and email in the AgentProject status, and
  fills them for a status from an older release.
- **R3** `workspacePod` sets both env vars on the runner and the agent
  container from the status.
- **R4** One default in the runner: `StartBranch` uses the Mirror identity,
  and `cmd/runner/main.go` reads the env vars through one helper.
- **R5** Docs: the security page, the status reference and this spec.

## Non-goals

- A per-project identity override in the AgentProject spec.
- GPG or SSH signing of agent commits.
- Rewriting the author of commits already pushed.
- Commits the model makes by running `git commit` itself outside the runner.

## Steering log

- **R1–R4 landed in one merge request** (`bot-git-identity`).
- **R5 landed in this merge request** (#874), after the code merge request, so
  the docs describe shipped behaviour (D874-P1). The commit identity is in
  [Security](../concepts/security.md#commit-identity) next to the token
  material (D874-P2), and the status fields are in
  [Status and history events](../reference/status-and-history.md#botgitname-and-botgitemail).
- **Narrowed:** none.
- **Acceptance check:** the commits of this merge request's branch were made
  before the release that carries R1–R4 ran in the cluster, so
  `git log --format='%an <%ae> / %cn <%ce>'` on them still shows the old
  identity (`ai-operator@noreply.local` on the start commit). The check
  applies to the first autopilot merge request made after that release.

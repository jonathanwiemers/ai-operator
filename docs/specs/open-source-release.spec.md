# Open-source release

## Problem

ai-operator is private on GitLab (`jo.wiemers/ai-operator`) and on the GitHub
mirror (`jonathanwiemers/ai-operator`). It is ready to be used by others, and a
blog post on `wiemers.dev` (repo `jo.wiemers/web`) should announce it. Before
anything goes public, the repository and its history must be free of secrets,
and the docs should not read like the notes of one homelab.

## Audit result (2026-11-13)

- **History:** `gitleaks` (701 commits, `--all`) found nothing. `trufflehog`
  found 2 unverified hits, both false positives: the CI test database URL
  `postgres://postgres:postgres@postgres:5432/…` and a docs filename.
- **Deleted files:** `config/samples/secrets.yaml` only ever held
  `REPLACE_ME` placeholders. No `.env`, key, kubeconfig, state or dump file
  was ever committed. No blob over 300 kB.
- **Token-like strings:** only placeholders and test values
  (`glpat-test`, `glpat-secret`, `Bearer at-1`, `CLAUDE_CODE_OAUTH_TOKEN=...`).
  Private IPs are SSRF test cases.
- **Production code:** no homelab references except the Go module path and the
  API group `ai.wiemers.me` (kept, see Non-goals).
- **Homelab references** (not secrets): `netcup01`, `jo.wiemers/infra/cluster`,
  Zitadel, Bloop, kerf and USD spend figures in README/docs/chart/`hack/`
  values, ~30 test files, 24 of 28 specs and all of `docs/process/`.
- **Commit metadata:** author emails `jonathan@wiemers.me`,
  `ai-operator@wiemers.me`, `ai-operator@noreply.local`; accepted as public.
- **GitLab project settings:** issues, MRs, wiki, snippets enabled,
  `public_jobs: true`. MR and issue threads carry agent output that was never
  audited.

## Goals

- GitHub and GitLab repositories public, code and history unchanged.
- On GitLab: issues and merge requests stay members-only, CI job logs are not
  public, wiki and snippets off.
- `docs/process/` removed. README, `docs/`, chart README/values and `hack/`
  values use generic examples (`my-cluster`, `example/infra`, "an OIDC
  provider such as Zitadel"). Specs and tests: mechanical rename of the same
  terms, no rewording.
- Contributor basics present: `LICENSE` (Apache-2.0, exists), `SECURITY.md`
  (exists, check contact), `CONTRIBUTING.md` (new, short: MRs on GitLab, GitHub
  is a mirror, how to run tests).
- Blog post "ai-operator is open source" in `jo.wiemers/web` with screenshots
  of the Desk.

## Non-goals

- Rewriting history. Nothing secret is in it; homelab names remaining in old
  commits are accepted. A rewrite would break every tag, release, the GitHub
  mirror and open MRs.
- Renaming the API group `ai.wiemers.me` or the Go module path: breaking for
  the running cluster, no gain.
- Making GitLab issues, MRs or milestones public (possible later, after an
  audit of agent comments).
- Removing Zitadel from the docs: it is the OIDC provider the Desk is tested
  with, so it stays as a named, supported example.

## Constraints

- A: no changed production code beyond comments. A2: `< 50` production
  lines.
- The running cluster must keep working: no change to CRDs, chart defaults or
  image names.
- Screenshots show only the Bloop and ai-operator projects; views that show
  other projects are cropped or skipped.

## Approach

### A. Repository cleanup (ai-operator, 1 MR)

1. `git rm -r docs/process`; remove links to it.
2. `sed` pass over docs, chart, `hack/`, specs, tests:
   `netcup01` → `my-cluster`, `jo.wiemers/infra/cluster` → `example/infra`,
   Bloop/kerf project paths → `example/game`, `example/cli`. Specs: drop USD
   figures from real runs where they are anecdotes, keep pricing examples.
3. README/docs/chart: Zitadel stays as the tested OIDC provider (steer
   2026-11-13: it is a supported setup, not a homelab detail).
4. Add `CONTRIBUTING.md`; check `SECURITY.md` contact.
5. Re-run `gitleaks` on the branch; `make test`, `make helm-lint`, `make ui`.

### A2. Configurable never-targets (ai-operator, 1 MR, stacked on A)

`aiv1.NeverTargets` hard-codes one homelab project
(`jo.wiemers/personal/kubernetes`) in production code. It becomes cluster
configuration, like the allow-list:

1. Flag `--cross-repo-never-projects` (comma separated, parsed by
   `projectList`), chart value `crossRepo.neverProjects: []`, empty by
   default.
2. `ValidateCrossRepo(spec, allowed, never)` and
   `gitlab.NewPolicy(spec, allowed, never)` take the list; the package
   variable goes. The reconcilers get a `CrossRepoNever` field next to
   `CrossRepoAllowed`.
3. The runner's guard (agent pod) passes no never-list: its allow-list is
   only the task's companion, which the operator already validated with the
   never-list. The operator is the gate; the pod guard keeps the home-project
   and allow-list checks.
4. Tests: a configured never-project is dropped even when allowed; with an
   empty never-list the same project is valid; flag parsing. Docs:
   `configuration.md`, `design.md`, chart README.

Rollout: the cluster's allow-list is empty today (cross-repo is off), so
the release that drops the hard-coded entry changes nothing. Whoever turns
cross-repo on sets `crossRepo.neverProjects` in the same infra change.

### B. Go public (owner, after A is merged)

1. GitLab project settings: issues and MRs "Only project members", wiki and
   snippets off, "Public pipelines" off, then visibility Public.
2. GitHub: visibility Public. Check that Actions secrets stay secret
   (they do) and that the mirror still pushes.
3. Badges in README resolve without login.

### C. Blog post (web, 1 MR)

`src/content/blog/2026/2026-11-<dd>-ai-operator-open-source.md`, tags
`ai`, `agents`, `kubernetes`, `gitlab`, `open-source`.

1. What it is in two paragraphs, link to the October post for the long story.
2. What changed since October, each with a screenshot: Desk dashboard,
   milestone pilot, live agent chat with steering, pause card and
   self-healing, costs page, releases by decision.
3. Quick start (Helm install, one `AgentProject`), links to GitHub/GitLab.
4. What I'd like help with / where to open issues.

Screenshots: real Desk at `agents.wiemers.dev` in the owner's Chrome
(owner signs in), 1440×900 window, light theme, saved as WebP under
`public/images/blog/ai-operator/`. Mobile shot of one view if the mobile
milestone looks good.

## Affected files/components

- ai-operator: `docs/process/**` (deleted), `README.md`, `docs/*.md`,
  `docs/specs/*.md`, `charts/ai-operator/{README.md,values.yaml}`,
  `hack/*.yaml`, `*_test.go` string literals, `CONTRIBUTING.md` (new).
- GitLab and GitHub project settings.
- web: one post, `public/images/blog/ai-operator/*`.

## Risks / trade-offs

- Test renames could break golden files: rerun `make test`, regenerate goldens
  only where the diff is the rename.
- Homelab names stay in history and in GitLab MR titles: accepted.
- Screenshots may show issue titles of other projects in shared views
  (dashboard, costs): crop or filter, review each image before commit.
- Publishing is outward-facing and hard to undo: B is done by the owner, or by
  the agent only after an explicit go.

## Validation / test plan

- `gitleaks git --log-opts=--all` and `gitleaks dir .` clean on the cleanup
  branch.
- `git grep -E 'netcup|jo\.wiemers/infra|[Zz]itadel'` only in intended
  places.
- CI green; `make test helm-lint ui` locally.
- After B: logged-out browser can open both repos, cannot open GitLab issues,
  MRs or job logs.
- Blog: `pnpm build`, check post renders, images load, no other project
  visible.

## Delivery plan

1. ai-operator MR "docs: prepare for open-source release" (A). Docs/tests
   only, 0 production lines.
2. ai-operator MR "feat(crossrepo): configurable never-targets" (A2),
   stacked on A.
3. Owner flips visibility (B), with the checklist above.
4. web MR "feat(blog): ai-operator is open source" (C). Merged after B, so the
   links work on publish day.

| Step | MR | Status |
|------|----|--------|
| A | !427 | open, review |
| A2 | !428 (stacked on !427) | open, review |
| B | – | not started |
| C | – | not started |

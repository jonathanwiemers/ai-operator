# ai-operator

A Kubernetes operator that lets coding agents work through your GitLab board.

1. Write a spec into a **milestone** description and add `/agent spec`.
   A spec agent reads the spec and the code and creates one **draft issue per
   merge request**. Each issue has a plan, acceptance criteria and questions
   for you.
2. Answer the questions in the comments, then move the issue to
   **`agent::ready`**. An implementing agent works on it in its own pod, the
   **bugbot and security reviewers** check the diff, and the implementer
   fixes blocking findings. Then the agent pushes `agent/<iid>-<slug>` and
   opens a **draft MR**. The issue moves to `agent::review`, or to
   `agent::needs-input` if the agent has questions.
3. Comment `/agent <what to change>` on the issue or the MR to get another
   round. Close the issue or move it to `agent::done`, and the pod and
   workspace are deleted.

Agents run headless agent CLIs. Each role can use **Claude Code** or
**Cursor CLI**, with your **subscriptions** or with API keys.

See [docs/design.md](docs/design.md) for the architecture, lifecycle, token
strategy and roadmap.

## Quick start

```sh
# CRDs, RBAC and the operator
make docker-build IMG=… AGENT_IMG=…   # or use published images
make deploy

# Namespace for your agents
kubectl create namespace agents
kubectl apply -f config/samples/agent_serviceaccount.yaml

# Credentials
kubectl -n agents create secret generic gitlab --from-literal=token=glpat-…
kubectl -n agents create secret generic agent-credentials \
  --from-literal=CLAUDE_CODE_OAUTH_TOKEN="…"   # from `claude setup-token`
  # --from-literal=CURSOR_API_KEY="…"          # for engine: cursor

# Connect a project (edit first)
kubectl apply -f config/samples/agentproject.yaml
```

Create the scoped labels `agent::draft`, `agent::ready`, `agent::working`,
`agent::needs-input`, `agent::review` and `agent::done` in GitLab. Put them
as columns on an issue board. All label names can be changed in
`spec.workflow`.

```sh
kubectl -n agents get agentprojects
kubectl -n agents get agenttasks     # type, issue, phase, result, runs, cost
kubectl -n agents get pods           # shop-spec-m3-checkout-v2, shop-42-add-paypal-button, …
```

## Agent image

`Dockerfile.agent` builds the base image: `ai-runner`, git, `claude` and
`cursor-agent`. Extend it with the toolchains your repositories need. The
agent must be able to build and run the tests.

## Development

```sh
make generate manifests   # deepcopy, CRDs, RBAC (controller-gen)
make test                 # unit tests incl. an end-to-end runner test against a local git repo
make run                  # run the operator against your current kubeconfig
```

Layout:

| Path | What |
|------|------|
| `api/v1alpha1` | `AgentProject`, `AgentTask` types and defaults |
| `internal/controller` | project controller (GitLab → tasks), task controller (pod/PVC lifecycle) |
| `internal/runner` | in-pod runner: spec / implement / review pipelines, engines (claude, cursor), git |
| `internal/gitlab` | minimal GitLab REST client and an in-memory fake for tests |
| `cmd/operator`, `cmd/runner` | binaries |
| `config/` | CRDs, RBAC, deployment, samples |

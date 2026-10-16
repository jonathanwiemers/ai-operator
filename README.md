# ai-operator

A Kubernetes operator that lets coding agents work through your GitLab board.

1. **Plan.** The milestone description holds the overarching spec. Add
   `/agent spec` to it, and a spec agent breaks it down into **one sub-issue
   per merge request**. Each sub-issue gets a self-contained sub-spec,
   acceptance criteria, questions for you and a profile label that says which
   agent should implement it (`agent-profile::fast` or `agent-profile::strong`).
2. **Implement.** Answer the questions, then move the issue to
   **`agent::ready`**. In a dedicated pod, the agent of the issue's profile
   implements it. **Bugbot and security reviewers** check the diff, and
   blocking findings are fixed. Then the agent pushes `agent/<iid>-<slug>` and
   opens a **draft MR**.
3. **Iterate.** Comment `/agent <what to change>` on the issue or the MR to get
   another round. Close the issue or move it to `agent::done`, and the pod and
   workspace are removed.

The agents are headless CLIs. Each role can use **Claude Code** or
**Cursor CLI**, with your **subscriptions** or with API keys. Strong models
are used only where they pay off: planning and `strong` issues. Reviews and
`fast` issues run on Sonnet or Cursor `auto`.

> **Status:** first version (`v1alpha1`). Unit and end-to-end tests pass. It
> has not yet been run against a real cluster, a real GitLab instance or the
> real agent CLIs.

## Documentation

| | |
|-|-|
| [docs/usage.md](docs/usage.md) | the GitLab workflow: labels, milestone specs, commands, lifecycle |
| [docs/configuration.md](docs/configuration.md) | installation, secrets, `AgentProject` reference, engines, profiles, agent image |
| [docs/design.md](docs/design.md) | architecture, CRDs, pod topology, token economy, security, roadmap |

## Quick start

```sh
# CRDs, RBAC and the operator (build and push your own images first, or use published ones)
make docker-build IMG=… AGENT_IMG=…
make deploy

# Namespace and service account for agent pods
kubectl create namespace agents
kubectl apply -f config/samples/agent_serviceaccount.yaml

# Credentials
kubectl -n agents create secret generic gitlab --from-literal=token=glpat-…
kubectl -n agents create secret generic agent-credentials \
  --from-literal=CLAUDE_CODE_OAUTH_TOKEN="…" \
  --from-literal=CURSOR_API_KEY="…"
  # CLAUDE_CODE_OAUTH_TOKEN comes from `claude setup-token`

# Connect a project (edit it first)
kubectl apply -f config/samples/agentproject.yaml
```

Then create the labels in GitLab ([usage.md](docs/usage.md#one-time-setup-in-gitlab))
and add `/agent spec` to a milestone.

```sh
kubectl -n agents get agenttasks   # type, issue, phase, result, runs, cost
kubectl -n agents get pods         # shop-spec-m3-checkout-v2, shop-42-add-paypal-button, …
```

## Development

Requires Go 1.26.

```sh
make generate manifests   # deepcopy, CRDs, RBAC (controller-gen)
make test                 # unit tests + end-to-end runner test against a local git repo
make run                  # run the operator against your current kubeconfig
```

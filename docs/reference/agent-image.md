# Agent image

The agents work in a container image that has the agent CLIs, git and the
tools to install your project's toolchains. This page says what the image
holds, how to extend it, and how to limit where agent pods may connect with
a network policy.

## Example

Extend the image with a tool your project needs and has no pin for:

```dockerfile
FROM ghcr.io/jonathanwiemers/ai-operator-agent:2026.10.1
USER root
RUN apt-get update && apt-get install -y golang-go && rm -rf /var/lib/apt/lists/*
USER 1000
```

Then point the project at it:

```yaml
spec:
  agents:
    image: registry.example.com/my-agent:2026.10.1
```

Without `spec.agents.image`, a project uses the agent image of the
installed release (see [`spec.agents`](agents-and-models.md#specagents)).

## What the image holds

- The runner, git, `make`, a C compiler and linker (`gcc`, `libc6-dev`),
  and the `claude` and `cursor-agent` CLIs. The build
  arguments `INSTALL_CLAUDE` and `INSTALL_CURSOR` leave a CLI out.
- `mise` and `rustup`, pinned and checksum-checked (build arguments
  `MISE_VERSION` and `RUSTUP_VERSION`). With them the agent installs the
  toolchains your default branch pins in `.mise.toml`, `.tool-versions` or
  `rust-toolchain.toml`. Nothing is installed on its own: the agent runs
  the install when it needs a tool. See
  [Toolchains the agent installs](../guides/while-the-agent-works.md#toolchains-the-agent-installs).

Toolchains go to `/workspace/home/.toolchains`, on the workspace, so the
next run of the same issue finds them. Extend the image for tools without a
pin, or to save the download per issue.

## Keep the image in your project

Keep the Dockerfile of your agent image in the project's own repository,
next to the pins it bakes in (`rust-toolchain.toml`, `.mise.toml`), and
build it in the project's pipeline. A change to a pin then rebuilds the
image in the same merge request.

The image carries the runner of the agent image it starts `FROM`. Keep
that tag at the version of the installed release: when you upgrade the
operator, bump the `FROM` tag, build, and point `spec.agents.image` at the
new tag. Renovate's Dockerfile manager finds the tag on its own. Install
toolchains as root outside `$HOME` (for example `/opt`), make them
readable for uid 1000, and put them on `PATH`. For a preinstalled Rust
toolchain, set `RUSTUP_HOME` to where you installed it and leave
`CARGO_HOME` on the workspace, where cargo writes its caches.

## How the pod runs

The same image runs both containers of an agent pod: the runner, which
talks to GitLab and holds the token, and the agent container, where the
model works (see [Security](../concepts/security.md)). Pods run as uid
1000, not as root, with all capabilities dropped. In the agent container
`HOME` is on the workspace (`/workspace/home`), so CLI sessions survive pod
restarts. Install tools outside `$HOME` when you extend the image.

## Network policy

The Helm chart can add a NetworkPolicy to every namespace in
`agent.namespaces`. It allows no ingress, allows DNS, and allows egress
everywhere except cloud metadata endpoints and private networks.

The runner needs the Kubernetes API, which usually has a private address,
so allow it, and any self-hosted GitLab or package mirror:

```sh
kubectl get endpoints kubernetes -o jsonpath='{.subsets[*].addresses[*].ip}'
helm upgrade ai-operator … \
  --set agent.networkPolicy.enabled=true \
  --set 'agent.networkPolicy.allowedCIDRs={10.0.0.10/32,10.20.0.0/24}'
```

NetworkPolicies need a network plugin that enforces them, such as Calico
or Cilium. The values (`agent.networkPolicy.*`) and their defaults are in
the [chart values](../../charts/ai-operator/README.md#values).

## Next

- [Agents and models](agents-and-models.md): resources, memory and the
  credentials of the agent pods.
- [Security](../concepts/security.md): what the model can and cannot reach.
- [AgentProject](agentproject.md): the other field groups.

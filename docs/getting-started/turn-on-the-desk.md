# Turn on the Desk

The [Desk](../concepts/glossary.md#desk) is the operator's web UI. You
answer the agents' [prompts](../concepts/glossary.md#prompt) there, ask the
[pilot](../concepts/glossary.md#pilot) about a milestone, chat with an agent,
and watch runs, CI and costs. This page shows the shortest way to turn it
on: an OIDC client for the login, one Secret, a few chart values, and one
line in your AgentProject. Then you log in.

It builds on [Install](install.md): the operator runs, your project is
connected, and the database is added.

## What you need

- **A host name** for the Desk, such as `ai.example.com`. The Desk is served
  at the root of its own host name, not under a path.
- **A Gateway** (Kubernetes Gateway API) that serves the host name with TLS.
  The chart adds an HTTPRoute to it.
- **An OIDC provider** for the login, such as Zitadel, Keycloak, Dex,
  Authentik or Entra ID. The provider also tells the Desk each user's roles,
  which decide what they see.

## 1. Create the OIDC client

Create a client (an application) at your provider with:

- redirect URI `https://ai.example.com/oauth2/callback`;
- post-logout redirect URI `https://ai.example.com/`.

The Desk uses the authorization code flow with PKCE, so a public client
without a secret works. A confidential client with a secret works too.

### Zitadel

1. Create a project, for example `ai-operator`, and add its **roles**, for
   example `ai-operator-admin`.
2. In the project's settings, turn on **Assert Roles on Authentication**, so
   the roles come with the login. Optionally turn on **Check authorization
   on Authentication**, so users without a role in the project cannot log
   in at all.
3. Add an application of type **Web**, authentication method **PKCE** (or
   **Code** with a client secret), with the redirect URIs above. Allow the
   **Refresh Token** grant type, so logins stay alive while you use them.
4. Grant the roles to your users under *Authorizations*.

Zitadel sends the roles in the claim `urn:zitadel:iam:org:project:roles`.
The values in step 3 read them from there.

### Other providers

The Desk reads the roles with an expression over the token's claims. Pick
the one for your provider and put it into `ui.oidc.claims.roles` in step 3:

| Provider | `roles` expression |
|----------|--------------------|
| Dex, Authentik and most others | `has(claims.groups) ? claims.groups : []` (the default) |
| Keycloak, realm roles | `has(claims.realm_access) ? claims.realm_access.roles : []` |
| Keycloak, client roles | `claims.resource_access["ai-operator"].roles` |
| Entra ID, app roles | `has(claims.roles) ? claims.roles : []` |

If your provider sends the claim only for an extra scope (`groups` for Dex),
add it to `ui.oidc.scopes`.

## 2. Create the Secret

The Desk needs a session key, which encrypts the login cookies, and for a
confidential client the client secret:

```sh
kubectl -n ai-operator-system create secret generic ai-operator-ui \
  --from-literal=session-key="$(openssl rand -hex 32)" \
  --from-literal=client-secret="…"        # confidential clients only
```

Changing the session key logs everyone out.

## 3. Set the chart values

Add the `ui` block to your `values.yaml`, next to the `agent` and
`research` blocks from [Install](install.md), and run the Helm command (or
let Flux apply it) again:

```yaml
ui:
  enabled: true
  externalURL: https://ai.example.com
  oidc:
    issuerURL: https://acme.zitadel.cloud
    clientID: "291833245810002@ai-operator"
    existingSecret: ai-operator-ui
    claims:
      roles: 'claims["urn:zitadel:iam:org:project:roles"]'
  authorization:
    rules:
      - roles: [ai-operator-admin]     # every project
        transcript: true               # live transcripts of the runs
        chat: true                     # answer prompts, chat with agents
  chat:
    writerSecret:
      name: research-chat-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do
  httpRoute:
    enabled: true
    parentRefs:
      - {name: public, namespace: gateway-system, sectionName: https}
```

What the values do:

- **`externalURL`** is the address you open. The HTTPRoute takes its host
  name from it.
- **`oidc`** names your client and the Secret from step 2. `claims.roles` is
  the expression from step 1.
- **`authorization.rules`** decide who sees what. Nobody sees anything
  without a rule. A user with one of a rule's roles sees the rule's
  projects (all of them when the rule names none). `chat: true` lets them
  answer prompts and chat with the agents.
- **`chat.writerSecret`** is the chat writer login from
  [Add the database](install.md#add-the-database). Without it, the Desk shows
  prompts but cannot take answers.

The Desk pod must reach your OIDC provider and the Kubernetes API. Every
`ui` value, such as a NetworkPolicy for the pod, is in the
[chart README](../../charts/ai-operator/README.md#web-ui), and the access
rules are explained in [Access rules](../guides/desk.md#who-sees-what).

## 4. Turn on chat in your project

The operator only acts on answers and messages from the Desk for projects
that allow it. Add `chat: true` under `agents` in your `shop.yaml` and apply
it again:

```yaml
spec:
  agents:
    credentialsSecretName: agent-credentials
    chat: true
```

```sh
kubectl apply -f shop.yaml
```

## 5. Log in

Open `https://ai.example.com` and sign in at your provider. The Desk opens
on *Overview*, with your project's milestones. The rail on the left has
*Inbox* (everything that needs you, across projects), *Overview*, *Metrics*
and your projects.

Your user name is at the bottom of the rail. Note it: on your first
milestone you name it as an [owner](../concepts/glossary.md#owner), so you
may answer that milestone's prompts. By default the user name is your email
address, or the token's subject when the provider sends no email.

If you see no project, your roles match no rule. Hover over your user name
to see the roles the Desk got from your provider.

## Staying logged in

A login lasts as long as you use it. The Desk renews it in the background
with a refresh token, and reads your roles again each time, so a role you
lose at the provider is soon gone in the Desk too. That needs the scope
`offline_access`, which is in the default scopes; without it, a login ends
after a fixed time. When your provider is briefly down, you stay logged in.
*Sign out* ends the login at the provider too.

How long a login lasts is set by the `ui.auth` values in the
[chart README](../../charts/ai-operator/README.md#web-ui). Keep the Desk at
one replica: with more, a renewal on another pod can end your login.

## Without login

For a quick look on a test cluster, you can skip the OIDC client:

```yaml
ui:
  enabled: true
  auth:
    mode: none
```

```sh
kubectl -n ai-operator-system port-forward deploy/ai-operator-ui 9080
# open http://127.0.0.1:9080
```

The Desk then listens inside its pod only, so only `kubectl port-forward`
reaches it, and everyone who can port-forward to it sees every project. The
chart creates no Service in this mode and refuses to combine it with
`ui.httpRoute`. Your user name is `anonymous`. With the chat writer login
and `chat: true` in your project you can still answer prompts, if the
milestone names `anonymous` as its owner.

## Next

- [Your first milestone](first-milestone.md): write a short spec, watch it
  get planned and answer a prompt.
- [The Desk](../guides/desk.md): each view, and who sees what.
- [Steer from the Desk](../guides/steer-from-the-desk.md): chat, prompts,
  pause cards and the queue.
- [Glossary](../concepts/glossary.md): the terms on this page.

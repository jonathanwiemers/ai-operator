# Web UI

A read-only web UI shows what the agents are doing: every project with its
tasks per stage, a board per milestone, autopilot queues, and each task's
runs, review, CI and pod. Pages update live. Users log in with OpenID Connect
(Zitadel, Keycloak, Dex, Entra ID, …), and their roles decide which projects
they see.

The UI is off by default. It is a separate Deployment (`<release>-ui`, binary
`/ui` in the operator image) with its own service account that can only read
`AgentProjects` and `AgentTasks`. It never sees the GitLab tokens or the agent
credentials. Everything it shows comes from the resources' status, and links
lead to GitLab; only with your GitLab account connected (see
[Steering](#steering)) does it read comments and post them, the milestone
chat included, with your own GitLab token.

## What it shows

| Page | Content |
|------|---------|
| **Projects** | every project you may see: tasks per stage, autopilot milestones and their progress, usage limits, suspension, last sync, model spend |
| **Project** | one board per milestone, newest first. The columns are *In progress* (working, waiting for capacity or a usage limit), *Needs you* (questions, red CI after the fix rounds, failed, blocked), *CI* and *Review*; then the milestone's plan, the autopilot pause reason, and issues without a milestone |
| **Milestone** | a planned milestone (linked from its project board, `/projects/<namespace>/<name>/milestones/<iid>`): autopilot phase and pause reason, the queue with states, merge requests and task pages, the pilot's autonomy and open proposals, the chat on its tracking issue, and the milestone's board |
| **Task** | stage, phase, current step, runs, last result and its summary, spend, branch and last push; review findings; pipeline, fix rounds and failed jobs; out-of-memory restarts; usage limit deferrals; the pod and how to follow its logs. Spec tasks add the plan with dependencies and the autopilot queue with merge requests and decisions |

With GitLab connected (see [Steering](#steering)), a task page has a
**Conversation** panel: the comments of its issue, merge request and
tracking issue, and, with the `steer` permission, a box to write comments in
your own GitLab name.

A milestone page has the same panel as its **Chat**: the comments of the
milestone's tracking issue and, with `steer`, a box whose messages go to the
pilot as `/pilot …` (the UI adds the prefix). Only the milestone owners'
instructions count, as in GitLab; answer a proposal with `yes P<n>`.

With the `transcript` permission, a task page also has a **Live** panel: the
running pod's steps and the agent's session as they happen (see
[Live transcript](#live-transcript)).

An autopilot that paused over an issue's run, review or CI and is **waiting**
for a run that is working on that issue now shows as *Autopilot waiting for
#N*: it continues by itself once the issue is reviewed and green. A pause
that needs a human shows as *Autopilot paused* with its reason. While a run
works on a task, its card marks the CI result as the last push's.

A task's **stage** is derived from its status the same way the operator moves
the GitLab labels; the hint on each badge explains it. After a push the task
stays in *CI* until the operator has followed that commit's pipeline, for at
most `spec.ci.noPipelineTimeout` plus one poll interval (unless the project
has `spec.ci.disabled`).

## Enable it

You need a host name for the UI, a Gateway (Gateway API) that serves it with
TLS, and an OIDC client.

### 1. Create the OIDC client

Redirect URI: `https://<host>/oauth2/callback`. Post-logout redirect URI:
`https://<host>/`. The UI uses the authorization code flow with PKCE, so a
public client (no secret) works; a confidential client works too.

#### Zitadel

1. Create a project, e.g. `ai-operator`, and add its **roles**, e.g.
   `ai-operator-admin` and `shop-devs`.
2. In the project's settings, turn on **Assert Roles on Authentication**. The
   roles then come with the login (the UI also reads the userinfo endpoint,
   which is where Zitadel puts them unless the application has *User roles
   inside ID Token* set). Optionally turn on **Check authorization on
   Authentication**, so users without a role in the project cannot log in at
   all.
3. Add an application of type **Web**, authentication method **PKCE** (or
   **Code** with a client secret), with the redirect URIs above.
4. Grant the roles to users (*Authorizations*).

Zitadel puts the roles into the claim `urn:zitadel:iam:org:project:roles`, a
map from role to the organizations that granted it. The UI takes a map's keys
as the roles:

```yaml
ui:
  oidc:
    claims:
      username: claims.preferred_username
      roles: 'claims["urn:zitadel:iam:org:project:roles"]'
```

#### Other providers

| Provider | `roles` expression |
|----------|--------------------|
| Dex, Authentik, most others | `has(claims.groups) ? claims.groups : []` (the default) |
| Keycloak, realm roles | `has(claims.realm_access) ? claims.realm_access.roles : []` |
| Keycloak, client roles | `claims.resource_access["ai-operator"].roles` |
| Entra ID, app roles | `has(claims.roles) ? claims.roles : []` |

Add the scope that makes your provider send the claim (e.g. `groups` for Dex)
to `ui.oidc.scopes`.

### 2. Create the Secret

```sh
kubectl -n ai-operator-system create secret generic ai-operator-ui \
  --from-literal=session-key="$(openssl rand -hex 32)" \
  --from-literal=client-secret=…        # confidential clients only
```

The session key encrypts the login cookies. Changing it logs everyone out.

### 3. Set the chart values

```yaml
ui:
  enabled: true
  externalURL: https://ai.example.com
  oidc:
    issuerURL: https://acme.zitadel.cloud
    clientID: "291833245810002@ai-operator"
    existingSecret: ai-operator-ui
    claims:
      username: claims.preferred_username
      roles: 'claims["urn:zitadel:iam:org:project:roles"]'
  authorization:
    rules:
      - roles: [ai-operator-admin]          # every project
      - roles: [shop-devs]
        projects: ["agents/shop"]           # namespace/name of the AgentProject
      - roles: [team-b]
        projects: ["team-b/*"]
  httpRoute:
    enabled: true
    parentRefs:
      - name: public
        namespace: gateway-system
        sectionName: https
  networkPolicy:
    enabled: true
    from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: gateway-system
```

The HTTPRoute takes its host name from `externalURL` (or
`ui.httpRoute.hostnames`) and forwards the whole host to the UI: the UI is
served at the root of its own host name, not under a path. All values are
listed in the [chart README](../charts/ai-operator/README.md#web-ui).

The UI pod needs to reach the OIDC provider (discovery, keys, token and
userinfo endpoints) and the Kubernetes API.

## Access rules

Nobody sees anything unless a rule grants it. A rule matches a user who has
**any** of its `roles`; `"*"` matches every logged-in user. `projects` are
`namespace/name` patterns with `*` and `?` (path globs, `*` does not cross the
`/`); without `projects` the rule grants every project. A user sees the union
of all matching rules. Seeing a project means seeing all its tasks.

Projects a user may not see do not exist for them: the API answers 404, and
no change events are sent for them.

`steer: true` lets its roles write comments from the UI (see
[Steering](#steering)); GitLab's own permissions apply on top.

`transcript: true` on a rule also shows its roles the live transcripts of the
projects' runs. Transcripts can quote code, issue text and CI logs, so they
are a grant of their own:

```yaml
ui:
  authorization:
    rules:
      - roles: [ai-operator-admin]
        transcript: true
        steer: true
      - roles: [shop-devs]
        projects: ["agents/shop"]       # boards and tasks, no transcripts
```

Roles are read at login and kept in the session, so a role change takes
effect at the next login, at the latest after `ui.auth.sessionTTL` (default
`12h`). A user without any matching role gets an empty project list that
names their roles, which helps to debug the claim mapping.

### Claim mapping

`ui.oidc.claims.username` and `ui.oidc.claims.roles` are
[CEL](https://cel.dev) expressions over the variable `claims`: the ID
token's claims, plus the claims only the userinfo endpoint returns (the ID
token wins where both have one). `username` must give a string. `roles` may
give a list of strings, a single string or a map whose keys are the roles. If
the roles expression fails because the claim is missing, the user has no
roles; a username that cannot be evaluated fails the login (the UI's log
says why).

## Live transcript

The **Live** panel on a task page follows the task's pod while it runs:

- the runner's steps (started, done, failed, with their results);
- which agent runs (role, engine, model, read-only);
- what the agent says and thinks (thinking: claude only), and the tools it
  calls with their main argument (command, file, pattern);
- with `spec.agents.logPrompts: true` on the AgentProject, every prompt:
  the role, the system prompt (with the project's instructions) and the
  prompt, each cut at 64 KiB. That includes what autopilot, CI fix runs,
  revisits and merge conflicts add to it.

The UI reads the pod's logs through the Kubernetes API, and passes on only
known kinds of lines of the current run, with a fixed set of fields, never a
raw log line. If a container restarts (out of memory), its log is followed
again. A pod stops a minute after its run (`lifecycle.idleTimeout`); after
that the panel is empty, and the run log of the last run is on the merge
request. Each new run opens a new stream.

The lines are what the containers logged, not signed: anything running in
the agent container could print lines that look like entries. They are shown
as text only, never as HTML.

The chart lets the UI's service account read `pods` and `pods/log` in each
namespace of `agent.namespaces` (Roles, nothing cluster-wide). An
AgentProject in a namespace that is not listed there has no transcripts:
its streams end with "pod not available".

## Steering

GitLab stays the place of record: comments written in the UI are GitLab
comments in **your** name, and the operator handles them like any other. A
plain comment reaches a running agent at its next step; `/agent …` starts a
run; `/agent use …` and `/agent effort …` change model and effort; on an
autopilot milestone's tracking issue, `/agent pause|resume|skip`. All
commands: [commands.md](commands.md).

### Set it up

1. Create an OAuth application on the GitLab your projects live on (for
   gitlab.com: *Group → Settings → Applications*, or *User settings →
   Applications*): **confidential**, scope **`api`** (GitLab has no narrower
   scope that may post comments), redirect URI
   `https://<host>/oauth2/gitlab/callback`.
2. Put its secret into the UI's Secret under `gitlab-client-secret` (or into
   another Secret named by `ui.gitlab.existingSecret`).
3. Set the chart values:

   ```yaml
   ui:
     gitlab:
       url: https://gitlab.com
       clientID: "<application id>"
     authorization:
       rules:
         - roles: [ai-operator-admin]
           steer: true
   ```

Each user then connects their account once with **Connect GitLab** on a
task page.

### One user, a fixed token

For a single user, the UI can use a fixed token of that user instead of an
OAuth application: a **fine-grained personal access token** (GitLab 19.2 and
later) limited to the group with your projects:

| Boundary | Permissions |
|----------|-------------|
| group (selected membership) | `read_project`, `read_work_item`, `create_work_item` (comments on issues and MRs are work item actions; it also allows creating issues), `read_merge_request` |
| user | `read_user` |

Put it into the Secret under `gitlab-token` and name its owner's UI user name
(what `oidc.claims.username` gives):

```yaml
ui:
  gitlab:
    url: https://gitlab.com
    tokenUser: ada@example.com
```

That user is connected without OAuth; nobody else may use the token (other
users can still connect their own accounts if `clientID` is set too). The
token expires (365 days at most) and must be renewed; a rejected token shows
as an error, not as a reconnect.

### How it works

- **Thread:** the task's issue, merge request and (on autopilot) tracking
  issue comments, read with your token, so GitLab decides what you see. The
  page looks again every 30 seconds and after every change of the task.
- **Comments:** posted with your token to the issue, MR or tracking issue
  you pick, at most 10 000 characters and 10 per minute. The buttons above
  the box start a comment with the common commands. The UI logs who posted
  where (user, GitLab user, project, target, note id), never the text.
- **Milestone chat:** a message to the milestone's pilot goes to its
  tracking issue with your token, under the same grant and limits. The UI
  puts the project's pilot prefix (`workflow.pilotPrefix`, `/pilot`) in
  front unless the message starts with it, so `yes P3` arrives as
  `/pilot yes P3` (and `/agent pause` as `/pilot /agent pause`: post
  commands in GitLab). Whether it counts as an owner's instruction is the
  operator's decision, as for any comment in GitLab.
- **Tokens:** the access and refresh token live in an encrypted, `HttpOnly`
  cookie (like the login), bound to your UI user, refreshed when the access
  token runs out, never readable by the page's JavaScript. The connection
  ends 30 days after you made it, however often it was refreshed.
  **Disconnect** forgets them; revoke the application in GitLab to end it
  there too.
- **Targets are checked:** a comment goes to an MR only if its source branch
  is the task issue's work branch, and to a tracking issue only if it is the
  operator's tracking issue of the task's milestone (the task status, which
  holds their numbers, is written by the agent pods too).
- The rate limit counts per UI replica. The `gitlab-client-secret` key must
  exist in the Secret, or the UI pod does not start.
- Projects on another GitLab host than `ui.gitlab.url` get no thread and no
  box.

## Without login

For a quick look, or a cluster without an OIDC provider:

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

The UI then listens on `127.0.0.1` inside its pod only, so only
`kubectl port-forward` reaches it, and everyone who can port-forward to it sees
every project. The chart creates no Service in this mode and refuses to
combine it with `ui.httpRoute`; the binary itself refuses `none` on any
address but a loopback one, and answers only requests for `localhost`,
`127.0.0.1` or `[::1]`, so a web page cannot reach it through a DNS name that
points to 127.0.0.1 (DNS rebinding).

## Security

- **Separate, read-only identity.** The UI's service account may only `get`,
  `list` and `watch` AgentProjects and AgentTasks, and read pods and their
  logs in the agent namespaces (for transcripts). No Secrets, no GitLab
  token. The API returns views of the resources without the
  operator's bookkeeping and secret references.
- **Login.** Authorization code flow with PKCE, `state` and `nonce`; the ID
  token's signature, issuer, audience and expiry are verified. The provider's
  tokens are not kept: the session holds only the user name, the roles and
  the expiry.
- **Sessions** are AES-GCM encrypted cookies (`__Host-` prefix, `HttpOnly`,
  `Secure`, `SameSite=Lax`), so the UI keeps no state and replicas share
  logins through the session key. Logout clears the cookie and ends the
  session at the provider (RP-initiated logout) if it supports it. Since
  nothing is stored on the server, a copied cookie stays valid until it
  expires (`ui.auth.sessionTTL`); changing the session key ends all sessions.
- **Redirects.** After the login the browser only goes back to a path on the
  UI's own host; login errors reach the sign-in page as codes, never as
  text from the URL.
- **Browser protections.** A strict Content Security Policy (only the UI's
  own scripts, styles and API, no framing), HSTS, `nosniff`, and Go's
  cross-origin protection for requests that change state (logout). Links to
  GitLab must be `http(s)` URLs.
- **https only.** `externalURL` must use https (http only for `localhost`);
  TLS ends at the Gateway.
- **Deny by default** (see [Access rules](#access-rules)).
- **Writing only as the user.** Comments are posted with the user's own
  GitLab OAuth token (scope `api`) or their fixed fine-grained token, never
  with a shared or the bot's token,
  and only with the `steer` grant; they go through Go's cross-origin
  protection, a size and a rate limit, and leave an audit line. The tokens
  stay server-side in an encrypted cookie bound to the UI user. A stolen pair
  of cookies could post as that user until the GitLab connection expires or
  is revoked.

## API

The UI is a Vue app on top of a small JSON API, which other clients can use
with the same session cookie:

| Endpoint | Returns |
|----------|---------|
| `GET /api/v1/me` | user name, roles, auth mode |
| `GET /api/v1/projects` | the projects you may see, with task counts per stage |
| `GET /api/v1/projects/{namespace}/{name}` | a project with its tasks by milestone (`trackingIssueIID` where a milestone has a tracking issue) |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}` | a planned milestone: its spec task, the autopilot (`phase`, `pausedReason` as raw text, queue `items` with title, state, MR number and URL, and task), the `tracking` issue, the `pilot` (task, autonomy, open proposals, cost), its implement tasks and `permissions` (`gitlab`, `steer`: what the page may offer); 404 without a spec task |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/thread` | the comments of the milestone's tracking issue (`parts`), read with your GitLab token once the issue is confirmed as the operator's tracking issue of this milestone (409 otherwise); 409 when not connected, 404 without a tracking issue, 502 when GitLab fails to list the comments |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/notes` | posts `{"body": "…"}` to the milestone's pilot on its confirmed tracking issue in your GitLab name, with the pilot prefix in front unless the body starts with it; needs the `steer` grant (403, 404 without the project), 409 for an issue that is not the milestone's tracking issue, 429 over the rate limit |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}` | one task and its project |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/transcript` | server-sent events: `entry` per transcript line of the task's pod (what it logged so far, then live), `end` with the reason (`no pod`, `pod stopped`, `pod not available`, `session expired`); needs the `transcript` grant (403 without), counts as an event stream |
| `GET /api/v1/gitlab` | whether GitLab is configured and your account connected (`enabled`, `connected`, `username`) |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/thread` | the comments of the task's issue, MR and tracking issue (`parts`), read with your GitLab token; 409 when not connected |
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/notes` | posts `{"target": "issue"\|"mr"\|"tracking", "body": "…"}` in your GitLab name; needs the `steer` grant |
| `GET /api/v1/events` | server-sent events: `change` with the kind, namespace and name of a project or task that changed |

Change events carry no content; the app fetches what it shows again. The
stream sends a heartbeat every 25 seconds and ends when the session expires.
A user may have 20 streams open at a time (one per browser tab); more answer
429.
If your Gateway cuts long requests, the app reconnects and catches up; set
`ui.httpRoute.timeouts: {request: 0s}` to keep the stream open.

## Development

```sh
make ui        # npm ci + type-check + build into web/dist/app, embedded by go build
make run-ui    # UI without login on http://127.0.0.1:9080, against your kubeconfig
cd web && npm run dev   # live reload on http://localhost:5173, API proxied to :9080
```

The Go server is in `internal/ui` (`cmd/ui` wires it), the app in `web/src`.
Without `make ui` the binary serves only the API.

## Roadmap

1. **Into a running step.** Messages that reach a running claude step at once
   (`--input-format stream-json`), not only at its next step.
2. **Actions** for an `operator` access level, limited to what GitLab cannot
   do (e.g. restart a stuck pod, clear a usage limit, hibernate a task).
   GitLab stays the place where the work is steered.
3. **MCP** at `/mcp` on the same host, with tools on top of the same API and
   access rules. MCP clients authenticate with bearer tokens issued by the
   same provider (the UI then acts as an OAuth resource server and publishes
   `/.well-known/oauth-protected-resource`); the roles in those tokens go
   through the same claim mapping and rules.

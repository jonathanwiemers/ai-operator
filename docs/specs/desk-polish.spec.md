# Desk polish: version, phone Costs tab, no milestone strip, ⌘K search

Status: the milestone's spec, run by autopilot. Narrowed requirements and
requirements without an issue go into the [Steering log](#steering-log).

## Goal

1. The Desk shows which ai-operator release it runs, and when a newer one is
   rolling out.
2. On a phone, Costs is one tap away, like Inbox and Overview.
3. Milestone and project pages lose the strip of all other milestones under
   the header; navigation goes through breadcrumbs and the project page.
4. ⌘K (Ctrl+K) opens a search that jumps to any project, milestone, issue or
   merge request the user may see.

## Decisions (locked)

1. **One version for UI and operator.** The UI binary is built with
   `-X main.version=${VERSION}` like the operator; `ARG VERSION` moves before
   the UI build and the lost layer cache is accepted.
2. **`GET /api/v1/about`** for any logged-in user: `version` is the UI
   binary's, `release` the self project's newest release decision that is not
   Dropped. The chart passes `--self-project` to the UI. `/api/v1/me` is not
   changed.
3. **Where it shows.** The rail footer (also in the phone's rail sheet) shows
   `ai-operator <version>` linked to GitLab, with *Update to X rolling out*
   (newer, Waiting or Running) or *X is out — reload* (newer, Released).
   Polled, not live.
4. **The strip goes, on both pages.** Breadcrumbs and the project page's
   `MilestoneSections` stay. Switching milestones no longer keeps the tab.
5. **Costs in the phone bar:** Inbox, Overview, Projects, Costs (feature
   `history`), Research (feature `research`).
6. **⌘K palette** with the groups Projects, Milestones, Issues & MRs and
   Pages; `GET /api/v1/search?q=` (min 2 characters, max 20, title and
   `#iid`/`!iid` match, authz-filtered, newest first). No database, no
   GitLab call.
7. **No change** to CSP, system prompts, authorization rules or the session
   code.

Decisions taken while planning and implementing:

8. **Milestone releases are read from the tasks.** The `release` of `/about`
   comes from `status.releases` of the AgentProject and from the milestones'
   `status.autopilot.releases`, because milestone releases are not in the
   project's own list.
9. **Versionless records are skipped**, like Dropped ones: a decision without
   a version has nothing to compare or link. The newest is by calendar
   version, a tie goes to the later decision.
10. **`releasesURL`.** `/about` also returns the self project's GitLab
    releases page (`""` without a self project), so a `dev` build links to
    it and a version links to `<releasesURL>/<version>` before the release
    has a URL of its own.
11. **The account line sits in the rail footer only.** The phone's rail
    sheet renders the same footer, so the line is on every width without a
    second copy in the account menu.
12. **The palette fetches milestones itself** (`/milestones?state=all`, once
    on first open): the shell does not load them, and finished milestones
    stay reachable.
13. **`/search` answers 400 below 2 characters**; the palette does not ask
    for shorter terms and treats errors as no results.

## Requirements and where they were implemented

| Requirement | Implemented in |
| --- | --- |
| R1 UI built with the version, `--self-project` for the UI | `Dockerfile`, `cmd/ui/main.go`, chart `ui-deployment.yaml` |
| R2 `GET /api/v1/about` | `internal/ui/about.go`, `web/src/generated/views.ts`; [API](../ui.md#api) |
| R3 about line and update notes | `web/src/about.ts`, `RailNav.vue`, `App.vue`; [Navigation](../ui.md#navigation) |
| R4 milestone strip removed | `web/src/views/Stream.vue`; [Navigation](../ui.md#navigation) |
| R5 Costs in the phone bottom bar | `web/src/App.vue`; [Navigation](../ui.md#navigation) |
| R6 `GET /api/v1/search` | `internal/ui/search.go`; [API](../ui.md#api) |
| R7 palette, shortcut, buttons | `web/src/components/SearchPalette.vue`, `searchItems.ts`, `searchPalette.ts`, `App.vue`; [Search palette](../ui.md#search-palette) |
| R8 docs | `docs/ui.md`, chart README, this file |

## Steering log

- R8 is documentation only, no release after it.

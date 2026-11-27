# Desk: Milestones and Issues first, a Timeline tab, and what each project builds

Status: archived. The milestone's spec, run by autopilot. The docs
requirement (R6, issue #900) wrote this file. Where this spec and the docs
disagree, the docs win: see [desk.md](../guides/desk.md) and
[web-ui.md](../contributing/web-ui.md#routes-and-tabs).

## Goal

1. A project page opens on its **Milestones** tab and a milestone page on its **Issues** tab. The Cockpit is the second tab of both. Every link to something in the Cockpit still lands there.
2. A milestone has its own **Timeline** tab after Issues. The Issues tab keeps the table and the *Lanes | Board* switch. An old `?view=timeline` link opens `?tab=timeline`.
3. The Overview at `/` opens on a **Projects** tab: one card per project with what it builds now, what comes next and whether it needs you. The tabs are *Projects · Timeline · Cockpit*; the Timeline tab shows only the timeline.
4. Old links keep working: `?tab=activity`, `stream`, `needs`, `?pilot=1`, the Overview's `?tab=overview` and a milestone's `?view=timeline`. Each opens where its content is now, and the address shows that tab.

## Decisions

1. **D1, default tabs.** Project: Milestones · Cockpit · Loose issues · Decisions · Metrics · Plans. Milestone: Issues · Timeline · Cockpit · Decisions · Metrics · Retrospective. Every Desk link to a Cockpit card or thread carries `?tab=cockpit`. The Cockpit keeps its Needs you badge.
2. **D2, Timeline as its own tab.** Shown only with the `history` feature. The Issues switch keeps Lanes (default) and Board. The Overview's Timeline tab shows only the timeline. No project Timeline tab.
3. **D3, Overview tabs.** *Projects* (default) · Timeline · Cockpit.
4. **D4, the project card.** Needs-you count; *Building now* (running or paused milestone, progress, running issues with their step); *Up next* (queued milestones in queue order, three plus "N more"); finishing and planned counts. Needs-you projects first, then running ones; idle projects fold into one line; *Recently done* below.
5. **D5, Timeline gets the full width.** The context panel is a slide-over at every width on a milestone's Timeline tab.
6. **D6, links from outside open the Cockpit by their hash.** On a milestone or project page a link without `?tab=` opens the Cockpit when its hash starts with `#prompt-` or `#issue-`, or is `#pinned`, or it has `?live=1`. The address is rewritten to `?tab=cockpit` and keeps the hash. `desklink` does not change.

## Requirements

- **R1.** Project and milestone pages open on Milestones and Issues; every alias that meant "the Cockpit" says so; an alias without a tab means the list's first tab.
- **R2.** The Desk's own links into the Cockpit name it; links from outside follow D6.
- **R3.** A milestone's Timeline tab, `milestoneTabsFor(m, history)`, `issuesView` with Lanes and Board only, and `?view=timeline` as an alias.
- **R4.** `web/src/projectCards.ts`, a pure module that builds the cards.
- **R5.** The Overview's Projects tab, `ProjectCards.vue`, and the Timeline tab without the milestone lists.
- **R6.** The docs and this spec.

Web only: no API, view struct or Go change, no new npm dependency, no new custom component.

## Steering log

One line per change of plan or narrowed requirement: `- YYYY-MM-DD, Rn (#issue): what changed and why; follow-up #n`.

- 2026-11-27, R4 (#896): `projectCards.ts` merged as `15c9a572`. No requirement narrowed.
- 2026-11-27, R1, R2 (#897): default tabs and `?tab=cockpit` links merged as `cad6bebe`. The D6 rule also covers `#problem-` hashes (old problem links open their thread in the Cockpit), which the spec did not list.
- 2026-11-27, R3 (#897): the Timeline tab merged as `ce1e50dc`. Without the history feature a `?tab=timeline` or `?view=timeline` link shows Issues, the first tab (D897-P2).
- 2026-11-27, R5 (#899): the Projects tab merged as `0ffb7928`. The bundle cap rose by 7 bytes (preload map of the Dashboard chunk), reported on its merge request; see the budget log in [web-ui.md](../contributing/web-ui.md#bundle-budget).
- 2026-11-27, R6 (#900): docs and this spec. The Desk screenshots are not retaken (#677, #678).

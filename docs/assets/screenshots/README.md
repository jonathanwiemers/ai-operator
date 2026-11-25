# Desk screenshots

Screenshots of the Desk, all on invented data (a shop on
`gitlab.example.com`, user `sam`). Do not edit them by hand: `make
docs-screenshots` renders them again, see
[Docs screenshots](../../contributing/web-ui.md#docs-screenshots). The current ones are shown in
[The Desk](../../guides/desk.md), the Metrics page in
[Steer from the Desk](../../guides/steer-from-the-desk.md#costs-at-a-glance).

| File | Page | Used in |
|------|------|---------|
| [`dashboard.webp`](dashboard.webp), [`dashboard-dark.webp`](dashboard-dark.webp) | Overview (`/`), its *Cockpit* (to retake) | README |
| [`project-stream.webp`](project-stream.webp) | Project page, its *Cockpit* with a milestone's pinned cards and the panel (to retake) | none until retaken; then [guides/desk.md, A project](../../guides/desk.md#a-project) |
| [`milestone-issues.webp`](milestone-issues.webp) | Milestone, *Issues* tab | [guides/desk.md, Issues](../../guides/desk.md#issues) |
| [`milestone-pilot.webp`](milestone-pilot.webp) | Milestone, *Cockpit* with the pause card pinned and the panel (to retake) | none until retaken; then [guides/desk.md, The pinned cards](../../guides/desk.md#the-pinned-cards), [guides/pauses.md](../../guides/pauses.md#when-it-pauses), [guides/pilot.md](../../guides/pilot.md#talk-to-it-in-the-desk) and [guides/steer-from-the-desk.md](../../guides/steer-from-the-desk.md#the-cockpit) |
| [`milestone-retrospective.webp`](milestone-retrospective.webp) | Milestone, *Retrospective* tab | [guides/pilot.md, The retrospective](../../guides/pilot.md#the-retrospective) |
| [`costs.webp`](costs.webp) | Costs (`/costs`) | [guides/steer-from-the-desk.md, Costs at a glance](../../guides/steer-from-the-desk.md#costs-at-a-glance) |

The ones marked "to retake" still show the Desk before the Cockpit, so no docs page shows them until they are retaken (the root README keeps its overview image). Run
`make docs-screenshots` on a machine with a full Chromium and `cwebp`, commit them, and put them back on the pages listed.

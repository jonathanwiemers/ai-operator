# Web UI: render GitLab-flavoured notes in the Desk

## Problem

The Desk shows comment bodies with the UI's markdown subset
(`web/src/markdown.ts`). That subset was written for agent output in research
results, where the server already refused raw HTML, tables and images. GitLab
notes are different: the operator's own comments use HTML and GFM that the
subset shows as text. A progress comment currently renders as:

- the marker `<!-- ai-operator:progress -->` as visible text,
- `<details open><summary>Run log <code>…</code>…</summary>` as text,
- the run-log table as one long paragraph of `| # | Step | … |`,
- entities as text (`Hold&#39;em`, from `html.EscapeString` in table cells),
- `<sub>` footers (MR descriptions) as text.

Comments written by people and agents use the same constructs, plus task
lists, strikethrough and rules.

## Goals

Render, as VNodes (never an HTML string, no `v-html`):

1. **HTML comments** dropped (block and inline, also across lines).
2. **`<details>` / `<summary>`** as native `details`/`summary`. The body
   holds markdown blocks, the summary holds inline content, nesting works,
   and the bare `open` attribute is kept.
3. **GFM tables**: a header row, a delimiter row (`---`, `:--`, `--:`,
   `:-:`), body rows, inline formatting in cells, `\|` in cells, column
   alignment. Wide tables scroll horizontally inside the card.
4. **Allowlisted inline HTML tags with no attributes:** `code`, `sub`, `sup`,
   `b`/`strong`, `i`/`em`, `del`/`s`, `kbd`, `br`. Each becomes a fixed
   element. A tag with attributes, or any other tag, stays text.
5. **Entities:** numeric (`&#39;`, `&#x27;`) and a small named set (`amp lt
   gt quot apos nbsp hellip mdash ndash`), decoded to text outside code.
6. **Thematic breaks** (`---`, `***`, `___`) as `hr`; **strikethrough**
   `~~x~~` as `del`; **task list items** `- [ ]` / `- [x]` as disabled
   checkboxes.

## Non-goals

- Images. They stay links, so the UI loads nothing from note bodies.
- Any other raw HTML (`div`, `img`, `a`, `span`, `style`, attributes,
  event handlers). `<script>` and similar stay visible text, as today.
- Setext headings (`text` followed by `---`). A `---` line under a
  paragraph is a rule, not an h2.
- GitLab references (`#12`, `!3`, `@user`) as links, emoji shortcodes, and
  math or mermaid blocks.
- Collapsing long notes in the stream. `open` is kept as written, so the
  progress comment shows open, the way GitLab shows it. If this turns out
  too long in the stream, that is a follow-up (a clamp with "show more").

## Constraints

- Security stays VNode-only. The renderer creates only fixed element names
  with no attributes copied from the input, except `open` (boolean),
  `start` (number) and `checked`/`disabled` on the checkbox. Links still go
  through `safeURL` and `ExtLink` (http/https, `nofollow`).
- Research blocks (`MarkdownBlock`) share the renderer. The server already
  strips HTML there, so their output changes only for tables, rules,
  strikethrough and task lists, which only make it better.
- Same bounded parsing as today (`maxDepth`, `maxSpan`): no input makes
  the renderer quadratic or recursive without bound.
- No new dependency (the web app has none besides Vue and vue-router).

## Approach

`web/src/markdown.ts`:

- Pre-pass: remove `<!-- … -->` spans (bounded, unclosed stays text).
- `blocks()`: new block starts for `<details…>` (collected up to the
  matching `</details>` by depth, summary taken from the first
  `<summary>…</summary>`, the rest parsed as blocks), tables (a pipe row
  followed by a delimiter row with the same cell count) and rules.
  `startsBlock` learns them, so they also end a paragraph.
- List items: a leading `[ ]` / `[x]` becomes a checkbox.
- `inline()`: `<tag>…</tag>` for the allowlist (matching close tag within
  `maxSpan`, else text), `<br>`, `~~…~~`, and entities in text runs.

`MarkdownBody.vue` / `MarkdownBlock.vue`: styles for `details`, `summary`,
`table` (borders, padding, `text-align` from the delimiter, a scroll
wrapper), `hr`, `kbd`, `sub`, task-list items.

## Affected files

- `web/src/markdown.ts`
- `web/src/components/stream/MarkdownBody.vue`,
  `web/src/components/blocks/MarkdownBlock.vue` (styles)
- `web/test/markdown.test.mjs`
- `docs/ui.md` (one line on what notes render)

## Risks / trade-offs

- A hand-written parser, not CommonMark: edge cases differ from GitLab
  (for example, a `---` under text). Accepted, as before. The common shapes
  the operator and agents write are covered by tests.
- Allowing inline tags widens what the renderer turns into elements.
  Mitigated by the fixed, attribute-free allowlist and by tests that check
  `<code onclick=…>`, `<a href>` and `<img>` stay text.
- About 150 production lines, over the 50-line budget (see delivery).

## Validation

- `web/test/markdown.test.mjs`: one case per construct (comment removed,
  details open/closed and nested, table with alignment, escaped pipe and
  inline code, cell-count mismatch stays text, allowlisted tags,
  tag with attributes stays text, `<script>` stays text, entities decoded
  and not in code, rule vs list `- - -`, strikethrough, task list). The
  case "tables are text" changes to the table output.
- A real progress comment and an MR-description footer as fixture cases.
- `make ui` (tests, type-check, build), then a browser check of a stream
  with a progress comment against the real handlers, light and dark.

## Steers

- 2026-11-02, from the maintainer: "make time be local, UTC is shown
  everywhere", same MR. The stream already formats every time in the
  browser's time zone. The time that looked like UTC was a progress
  comment's creation time (13:59 UTC, 14:59 CET) on a comment GitLab has
  edited for an hour since. So the stream's times now carry a tooltip with
  the full local date, time and zone (`<time datetime title>`), and the
  progress comment's edits are noted as a possible follow-up (show the last
  edit), not changed here.

## Delivery

One MR, `desk-markdown-notes`, about 150 production lines plus styles and
tests. Splitting it (comments, details, inline tags and entities first;
tables, rules, strikethrough and task lists second) is possible, but
neither half fixes the progress comment alone.

| MR | Status |
|----|--------|
| [!68](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/68) Desk: render GitLab notes | open |

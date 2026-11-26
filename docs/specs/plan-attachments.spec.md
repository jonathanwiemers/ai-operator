# Desk plans: attach screenshots and files, and the plan agent sees them

Status: archived. The milestone's spec, run by autopilot after "Desk: start a
plan from anywhere" ([plan-anywhere](plan-anywhere.spec.md)). The milestone
description in GitLab holds the full text with its file references; this file
keeps the goal, the decisions, the requirements, the constraints, the
non-goals, the acceptance and the [Steering log](#steering-log). Where this
spec and the docs disagree, the docs win: the flow is in
[Write a milestone](../guides/attach-files-to-a-plan.md),
the routes and the limits in
[Desk API](../reference/plan-attachments.md), the design in
[Design](../contributing/design.md#desk-plans) and the Composer in
[Web UI development](../contributing/web-ui.md#composer).

## Goal

1. In a plan's chat, an owner attaches up to 5 files to a message with the
   paperclip, by pasting or by dropping them on the composer. Images, PDF
   and plain text files up to 10 MiB each are accepted; other files get a
   clear error.
2. On **Send** the files upload to the project's GitLab uploads with the
   owner's GitLab token, and the message carries their links. The sent
   message shows a thumbnail per image and a chip per other file; clicking
   one opens it.
3. The plan agent sees the files: it opens images and PDFs with its Read
   tool and may embed the links in the draft.
4. The New plan page gets the same control for its first message.

## Decisions (locked)

1. **Storage: GitLab uploads, with the owner's token.** The Desk keeps no
   file bytes: no table, no retention job. Uploads of an abandoned plan stay
   in GitLab, and in a public project anyone with the link can open them.
2. **Upload on Send.** Files stay in the browser as chips until Send. If an
   upload fails, the message is not posted and the chips stay.
3. **An attachment is a link in the message text**: GitLab's `markdown`, one
   line per file after the text. No column, CRD field or view field.
4. **Types and limits** live once in `internal/attachments`; the server
   checks extension and sniffed content; the web copy is checked by a test.
5. **Composer control for plan chats only**, built on Nuxt UI.
6. **The New plan page** uploads through the chosen project's route before
   Start plan.
7. **The Desk shows attachments** as thumbnails and chips below the text,
   read through its own route with the viewer's GitLab token.
8. **One shared package**, `internal/attachments`, reused by the next
   milestone, "Planner and implementers see the screenshots linked in
   milestones, issues and comments".
9. **Which links count**: uploads of the same project, in three forms.
10. **What the plan agent gets**: files fetched each turn into
    `.plan-context/attachments/`, a `## Attachments` prompt section, and
    three sentences in the system prompt.

## Requirements

- **R1** GitLab client: `UploadFile` and `DownloadUpload`, with `gitlabfake`.
- **R2** `internal/attachments`: `Allowed`, `Find`, `Fetch`.
- **R3** `POST …/plans/uploads`.
- **R4** `GET …/plans/uploads/{secret}/{filename}`.
- **R5** The plan turn fetches attachments and lists them in the prompt.
- **R6** The Composer `attachments` prop.
- **R7** Show attachments in plan chat messages and the draft tab.
- **R8** The New plan page's first message.
- **R9** The docs and this spec.

## Constraints

- No new Go or npm dependencies; no new Kubernetes permissions.
- Task chats, the pilot, note composers and every agent prompt other than
  the plan turn's stay unchanged.
- The Desk never stores file bytes, and no log, audit line or error holds
  file content or an upload secret.

## Non-goals

- The planner and implementers reading attachments from milestones, issues
  and comments (the next milestone).
- Attachments in task chats, the pilot, research or GitLab note composers.
- Deleting uploads, resizing images, other file types, private files until
  the milestone exists, and inline images in the draft tab.

## Acceptance

- Paste a screenshot, add a `.log` and drop a PDF: three chips. Remove the
  PDF and Send: a thumbnail and a chip, and two new uploads in GitLab.
- A `.zip`, a 12 MiB PNG and a sixth file are refused with the file's name
  and the rule; a `.png` that is a text file is refused by the server.
- Without a connected GitLab, sending with a file asks to connect and posts
  nothing.
- The agent describes an attached screenshot and may embed its link in the
  draft; a later turn still lists a file only the draft links.
- On `/plans/new` the plan opens with the image in the first message.
- A viewer without `steer` sees thumbnails but no paperclip; without GitLab
  a chip reads "Connect GitLab to view".

## Steering log

Collected from the milestone's merge requests, as their commits record them
(the merge request numbers are in GitLab).

- **R1** `UploadFile` (multipart, not retried) and `DownloadUpload` (capped),
  in `gitlab.API` and `gitlabfake` (`b7144719`).
- **R2** `internal/attachments`: types and sniffing, the link finder, safe
  names and `Fetch` under caps (`06fe9d79`). `Fetch` takes the project as a
  parameter, since `Find` returns links of one project only.
- **R5** The plan turn fetches into `.plan-context/attachments/` and lists
  the files under `## Attachments` (`87bd8b79`). A link that cannot be
  fetched is listed with its reason; the turn never fails for it.
- **R3** and **R4** The two routes (`b807fa9f`); they are in the
  [Desk API](../reference/plan-attachments.md).
- **R6** The Composer prop and `AttachmentPicker` (`981266d4`). The file
  dialog's input is made in script, as Nuxt UI's `useFileUpload` does, so
  it is no raw control and no UI kit entry was needed.
- **R7** Thumbnails, chips and draft links (`fef0b18b`).
- **R8** Start plan on `/plans/new` uploads first (`ab546b14`).
- **R9** The docs and this spec. Defaults (types, 10 MiB, 5 per message, 20
  files and 50 MiB per turn) are stated once, in `reference/`
  (on the new page, not in desk-api.md as D838-P1 said; see below).
- **Narrowed: the `-/project/<id>/` link form in the web.** The server and
  the runner recognize all three forms. The web recognizes
  `<gitlab>/-/project/<id>/uploads/…` only when it knows the project's
  numeric ID, which the Desk's project view does not carry; such a link
  shows as plain text there. GitLab itself returns the relative form, so
  the Desk's own messages are unaffected. A follow-up issue is needed for
  the rest; none is open yet.
- **R9, page split (deviates from D838-P1).** The Desk API page and the guide were at their
  400-line cap, so the routes and the defaults moved to a new reference page,
  [Plan attachments](../reference/plan-attachments.md), and the guide section
  to [Attach files to a plan](../guides/attach-files-to-a-plan.md). The
  defaults are still stated once, in the reference; the Desk API page lists
  the two routes and links to it.

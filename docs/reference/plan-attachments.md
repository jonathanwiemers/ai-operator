# Plan attachments

The Desk lets you attach files to a plan message, and the planner reads
them. This page lists the two routes, the allowed types and the limits. The
[guide](../guides/attach-files-to-a-plan.md) shows the Desk side. The routes
are part of the [Desk API](desk-api.md#plans).


An attachment is a file in the project's GitLab uploads, linked from a plan
message. The Desk stores no file: the upload goes to GitLab with your GitLab
token, and the message carries the link GitLab returns. The New plan page
uses the same two routes with the project you pick. See
[Attach files to a plan](../guides/attach-files-to-a-plan.md)
for how this looks in the Desk.

```sh
# Upload a screenshot, then send its Markdown with the message.
curl -sS -X POST -b "$COOKIE" -F file=@header.png \
  "$DESK/api/v1/projects/agents/tools-foo/plans/uploads"
# {"name":"header.png","size":48213,"contentType":"image/png",
#  "url":"/uploads/<secret>/header.png",
#  "markdown":"![header.png](/uploads/<secret>/header.png)"}
```

`POST …/plans/uploads` takes one multipart part named `file`. It answers
`201` with `name` (the cleaned name), `size`, `contentType`, `url` and
`markdown`. Put `markdown` on its own line after your text in the message.
The audit log records the user, project, size and type, never the content.

| Status | When |
|--------|------|
| `400` | The type is not allowed, the content does not match the extension, the file is empty, or the request is not one `file` part. `field` is `file`. |
| `409` | You have not connected your GitLab account. |
| `413` | The file is larger than the size limit below. |
| `429` | You wrote to GitLab too often. |

`GET …/plans/uploads/{secret}/{filename}` reads a file of the project with
your own GitLab token, so you see only what your account may open. The
secret is 32 hex characters; any other path answers `404`. The answer has
the type the content sniffs as, `X-Content-Type-Options: nosniff`,
`Content-Security-Policy: sandbox` and `Cache-Control: private, max-age=3600`.
`Content-Disposition` is `inline` for images and PDF and `attachment` for
text files.

| Status | When |
|--------|------|
| `404` | GitLab has no such file, or the path is not an upload path. |
| `409` | You have not connected your GitLab account. |
| `413` | The file is larger than the size limit below. |
| `415` | The file is not of an allowed type. |

**Types and limits.** These are the defaults. They are stated here once;
the guide and the design page link to this list.

- Allowed types: PNG, JPG/JPEG, GIF, WebP and PDF, and the text files
  `.txt`, `.log`, `.md`, `.json`, `.yaml`/`.yml` and `.csv`. The server
  checks the extension and the content: an image or PDF must be that type,
  a text file must be plain text.
- A file is at most 10 MiB.
- A message carries at most 5 files, and the attachment links count toward
  the message's size limit (see [Limits](desk-api.md#limits)).
- The planner reads at most 20 files and 50 MiB per turn, newest first,
  from the links in the turn's messages and the plan's current drafts. It
  skips the rest and says so in its prompt.


## Next

- [Attach files to a plan](../guides/attach-files-to-a-plan.md): how the
  files look in the Desk.
- [Desk API](desk-api.md#plans): the other plan endpoints.
- [Glossary](../concepts/glossary.md): terms such as plan and Desk.

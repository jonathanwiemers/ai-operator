# Attach files to a plan

Show the planner what you mean. In a plan's chat, or in the first message
on the New plan page, you can attach a screenshot, a PDF or a log, and the
agent reads it. This page shows how, and where the files live.


Show the agent what you mean. In a plan's chat, or in the first message on
the New plan page, attach a screenshot, a PDF or a log:

1. Click the paperclip next to **Send**, paste an image from the clipboard,
   or drop files on the message box.
2. Each file shows as a chip with its name, size and a remove button.
   Remove one and nothing is uploaded. The page tells you why a file is
   refused, by its name.
3. Write your message, for example "the header overlaps the tabs on
   phones, see screenshot", and click **Send** (or **Start plan**).

The files upload when you send, one by one, with your own GitLab login. If
one fails, the message is not posted, the chips stay, and the error names
the file. Without a connected GitLab account, the box asks you to connect
it first.

Sent attachments show below the message: a thumbnail for an image and a
chip for any other file. Click one to open it in a new tab. If you cannot
steer the project, you see them but get no paperclip. If you have not
connected GitLab, a chip says *Connect GitLab to view*.

The agent opens the files with its Read tool, says in words what matters in
them, and may embed their links in the draft where whoever builds the
milestone needs to see the picture. The draft shows such a link as a
link, not as an inline image. A later turn still sees a file that only the
draft links.

The files live in the project's GitLab uploads, not in the Desk. In a
public project anyone with the link can open them. They stay in GitLab when
you delete the plan or the message. Types and limits are in the
[Plan attachments reference](../reference/plan-attachments.md).

## Next

- [Plan attachments reference](../reference/plan-attachments.md): the
  upload routes, the allowed types and the limits.
- [Write a milestone](write-a-milestone.md#draft-it-in-the-desk): the plan
  flow the files belong to.
- [Desk](../concepts/glossary.md#desk): the glossary entry for the web UI.

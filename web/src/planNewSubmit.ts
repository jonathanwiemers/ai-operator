// The submit of the New plan page (milestone "Desk plans: attach screenshots
// and files", R8): the files upload first, in order, then the plan is created
// with their links in the first message. A failed upload creates no plan.
import { bodyTooLong, uploadAll, withAttachments } from "./attachments.ts";

export type StartPlanResult<P> =
  { plan: P } | { error: string; status?: number };

export async function startPlan<P>(args: {
  files: File[];
  message: string;
  title: string;
  upload: (f: File) => Promise<{ markdown: string }>;
  create: (body: { message: string; title?: string }) => Promise<P>;
}): Promise<StartPlanResult<P>> {
  const { files, message, upload, create } = args;
  const title = args.title.trim();
  let body = message;
  if (files.length) {
    if (bodyTooLong(message))
      return { error: "The message is larger than 16 KiB." };
    const up = await uploadAll(files, upload);
    if ("error" in up)
      return { error: up.error, ...(up.status ? { status: up.status } : {}) };
    body = withAttachments(message, up.markdown);
    if (bodyTooLong(body))
      return {
        error: "The message with its attachment links is larger than 16 KiB.",
      };
  }
  return { plan: await create({ message: body, ...(title ? { title } : {}) }) };
}

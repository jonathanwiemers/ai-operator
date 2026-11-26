// uploadPlanFile: POST /projects/{namespace}/{name}/plans/uploads, one file
// as the multipart field "file", stored in the project's GitLab uploads with
// the user's token. It lives outside client.ts, which is in the initial
// bundle, as only the lazy plan chat uses it.
import { post } from './client'
import type { PlanUploadView } from './generated/views'

export function uploadPlanFile(namespace: string, name: string, file: File): Promise<PlanUploadView> {
  const form = new FormData()
  form.append('file', file, file.name)
  return post(`/projects/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/plans/uploads`, form)
}

// uploadPlanFile: POST /projects/{namespace}/{name}/plans/uploads, one file
// as the multipart field "file", stored in the project's GitLab uploads with
// the user's token. It lives outside client.ts, which is in the initial
// bundle, as only the lazy plan pages use it.
import type { PlanUploads } from './attachments'
import { getGitLabStatus, getProjects, post } from './client'
import type { PlanUploadView } from './generated/views'

export function uploadPlanFile(namespace: string, name: string, file: File): Promise<PlanUploadView> {
  const form = new FormData()
  form.append('file', file, file.name)
  return post(`/projects/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/plans/uploads`, form)
}

// loadPlanUploads is what the plan page needs to show attachments: the
// project's GitLab ref from its summary (none for a set's stand-in project:
// then only relative links count) and the viewer's GitLab connection. A
// failed status read counts as connected, so images still try to load.
export async function loadPlanUploads(namespace: string, name: string, returnTo: string): Promise<PlanUploads> {
  const [projects, status] = await Promise.all([getProjects().catch(() => []), getGitLabStatus().catch(() => null)])
  const p = projects.find((x) => x.namespace === namespace && x.name === name)
  const out: PlanUploads = { namespace, name, connected: !status || status.connected }
  // A project named by its numeric ID gives the /-/project/<id>/ form.
  const ref = p?.gitlab
  if (ref?.url && /^\d+$/.test(ref.project)) out.project = { url: ref.url, path: '', id: Number(ref.project) }
  else if (ref?.url && ref.project) out.project = { url: ref.url, path: ref.project }
  if (status && !status.connected && status.login) out.connect = `${status.login}?return=${encodeURIComponent(returnTo)}`
  return out
}

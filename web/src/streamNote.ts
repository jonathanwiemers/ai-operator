// useStreamNote is the state of a note composer (Composer.vue, mode note):
// the project stream's and the milestone Pilot tab's. It loads the user's
// GitLab status, offers the targets (stream.ts noteTargets) and posts the
// note; the composer only shows it (docs/ui.md, "Composer").
import { computed, reactive, ref, watch } from 'vue'
import { getGitLabStatus, LoginRequiredError, postMilestoneNote, postTaskNote } from './client'
import { keepThroughSignIn } from './signInDraft'
import { keepNoteTarget, noteDisabledReason, noteHint, notePlaceholder, noteTargets, type NoteTarget } from './stream'
import type { GitLabStatus, ProjectDetail } from './types'

export interface StreamNoteSource {
  namespace: string
  name: string
  project: ProjectDetail | null
  // The milestone whose pilot comes first (and is offered while idle).
  milestoneIID?: number
  // false: the user's roles may not comment.
  steer?: boolean
  // shown: the composer is on the page; the GitLab status loads the first
  // time it is.
  shown: boolean
}

export function useStreamNote(source: () => StreamNoteSource, onPosted?: () => void) {
  const status = ref<GitLabStatus | null>(null)
  const body = ref('')
  keepThroughSignIn(() => `note:${source().namespace}/${source().name}`, body)
  const sending = ref(false)
  const error = ref('')
  const target = ref<NoteTarget | null>(null)

  const targets = computed(() => noteTargets(source().project, source().milestoneIID))
  watch(targets, (list) => (target.value = keepNoteTarget(list, target.value)), { immediate: true })

  const disabledReason = computed(() => noteDisabledReason(status.value, source().steer, targets.value.length))
  // blocked: GitLab is not connected or the roles forbid comments, so a
  // card takes the box's place.
  const blocked = computed(() =>
    disabledReason.value && (!status.value?.connected || source().steer === false) ? disabledReason.value : '',
  )
  const connectUrl = computed(() => {
    const s = status.value
    if (!s || s.connected || !s.login) return ''
    const back = window.location.pathname + window.location.search
    return `${s.login}?return=${encodeURIComponent(back)}`
  })
  const hint = computed(() => disabledReason.value || noteHint(target.value))
  const placeholder = computed(() => notePlaceholder(target.value))

  async function send() {
    const t = target.value
    if (!body.value.trim() || sending.value || !t || !status.value?.connected) return
    const { namespace, name } = source()
    sending.value = true
    error.value = ''
    try {
      if (t.type === 'pilot') await postMilestoneNote(namespace, name, t.milestoneIID, body.value)
      else await postTaskNote(namespace, name, t.task, 'issue', body.value)
      body.value = ''
      onPosted?.()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      sending.value = false
    }
  }

  let loading = false
  watch(
    () => source().shown,
    async (shown) => {
      if (!shown || loading || status.value) return
      loading = true
      try {
        status.value = await getGitLabStatus()
      } catch (e) {
        if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
      } finally {
        loading = false
      }
    },
    { immediate: true },
  )

  return reactive({ body, sending, error, target, targets, disabledReason, blocked, connectUrl, hint, placeholder, send })
}

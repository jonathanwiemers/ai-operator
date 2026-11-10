import type { Tone } from './format.ts'
import { releaseStateLabel, releaseTone } from './releaseRows.ts'
import type { ReleaseView, StreamEvent } from './types'

// The release events of a milestone's stream (R12, decision 15):
// release.decided, release.started and release.ended
// (internal/history/differ.go). Why and reason are untrusted text; the
// component renders them as text.

type ReleaseEvent = Pick<StreamEvent, 'kind' | 'actor' | 'data'>

// releaseWho says who decided: "planner", "owner @jo", "the operator" for
// the final release and the sweep.
export function releaseWho(e: ReleaseEvent): string {
  const by = e.data.by || ''
  if (by === 'final' || by === 'sweep') return 'the operator'
  if (!by) return e.actor || 'someone'
  return e.actor ? `${by} ${e.actor}` : by
}

// releaseWhere names the release after its issue: " after #N", " of the
// milestone" for the final one, '' otherwise.
function releaseWhere(e: ReleaseEvent): string {
  if (e.data.final) return ' of the milestone'
  return e.data.afterIssue ? ` after #${e.data.afterIssue}` : ''
}

// releaseEventTitle is the event's headline: "Release decided by planner
// after #4", "Release started", "Released 2026.11.3", "Release failed".
export function releaseEventTitle(e: ReleaseEvent): string {
  switch (e.kind) {
    case 'release.decided':
      return `Release decided by ${releaseWho(e)}${releaseWhere(e)}`
    case 'release.started':
      return `Release${releaseWhere(e)} started`
    case 'release.ended':
      return releaseEndedTitle(e)
    default:
      return ''
  }
}

function releaseEndedTitle(e: ReleaseEvent): string {
  const where = releaseWhere(e)
  switch (e.data.state) {
    case 'Released':
      return e.data.version ? `Released ${e.data.version}` : `Released${where}`
    case 'AlreadyReleased':
      return e.data.version ? `Already released in ${e.data.version}` : `Already released${where}`
    case 'Failed':
      return `Release${where} failed`
    case 'NotNeeded':
      return e.data.final ? 'Final release not needed' : `No release needed${where}`
    case 'NotApplicable':
      return 'No release: the project does not tag'
    case 'Dropped':
      return `Release${where} dropped`
    default:
      return `Release${where} ended`
  }
}

// releaseEventText is the event's untrusted line: the why of a decision,
// the reason of an end; '' without one.
export function releaseEventText(e: ReleaseEvent): string {
  const s = e.kind === 'release.decided' ? e.data.why : e.kind === 'release.ended' ? e.data.reason : ''
  return (s || '').trim()
}

// releaseEventBadge is the state badge of an ended release, undefined for
// the other kinds.
export function releaseEventBadge(e: ReleaseEvent): { label: string; tone: Tone } | undefined {
  if (e.kind !== 'release.ended' || !e.data.state) return undefined
  const s = e.data.state as ReleaseView['state']
  return { label: releaseStateLabel(s), tone: releaseTone(s) }
}

// releaseEventIssues lists the issues a started release contains: "#3, #4".
export function releaseEventIssues(e: ReleaseEvent): string {
  return (e.data.issues ?? []).map((n) => `#${n}`).join(', ')
}

// isReleaseEvent: one of the three release kinds.
export function isReleaseEvent(e: Pick<StreamEvent, 'kind'>): boolean {
  return e.kind === 'release.decided' || e.kind === 'release.started' || e.kind === 'release.ended'
}

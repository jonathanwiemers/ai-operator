import { clockTime } from './format.ts'
import type { Deferral, GitLabRateLimitStatus } from './types'

// The Desk's view of GitLab's rate limit (R11 and R12 of
// docs/specs/gitlab-rate-limit.spec.md): a task, issue row or queue item
// deferred on it, and the bot token's usage in the project header. Times are
// local (clockTime).

// gitlabDeferred is whether d waits for GitLab's rate limit: block reason
// gitlab and a retry time still ahead.
export function gitlabDeferred(d: Deferral | undefined | null, now: number): boolean {
  return d?.blockReason === 'gitlab' && !!d.deferredUntil && Date.parse(d.deferredUntil) > now
}

// gitlabBadge is the badge of a deferred issue row or queue item ("GitLab
// limit · 07:42"), null unless gitlabDeferred.
export function gitlabBadge(d: Deferral | undefined | null, now: number): string | null {
  return gitlabDeferred(d, now) ? `GitLab limit · ${clockTime(d!.deferredUntil, now)}` : null
}

// deferralTail is the end of the task banner's sentence after the relative
// time: "(at 07:42, deferred 1×)."
export function deferralTail(d: Pick<Deferral, 'deferredUntil' | 'deferrals'>, now: number): string {
  return `(at ${clockTime(d.deferredUntil, now)}, deferred ${d.deferrals || 1}×).`
}

export interface GitLabUsage {
  observed: number
  limit: number
  // low: at or below the reserve, where background calls wait.
  low: boolean
  label: string
}

// gitlabUsage is the project header's usage bar ("GitLab API 63/100 this
// minute"), null without a reading or once its window reset more than a
// minute ago: then "this minute" would be wrong (D890-P1). Zero counts are
// omitted from the status, so a missing observed or remaining is 0.
export function gitlabUsage(r: GitLabRateLimitStatus | undefined | null, reserve: number, now: number): GitLabUsage | null {
  if (!r?.limit || !r.resetAt) return null
  const reset = Date.parse(r.resetAt)
  if (Number.isNaN(reset) || reset < now - 60_000) return null
  const observed = r.observed ?? 0
  return { observed, limit: r.limit, low: (r.remaining ?? 0) <= reserve, label: `GitLab API ${observed}/${r.limit} this minute` }
}

// gitlabLimitedLine is the header's line while the project's runs wait for
// the limit ("GitLab rate limit until 07:42"), null once it has passed.
export function gitlabLimitedLine(r: GitLabRateLimitStatus | undefined | null, now: number): string | null {
  const until = r?.limitedUntil
  if (!until || !(Date.parse(until) > now)) return null
  return `GitLab rate limit until ${clockTime(until, now)}`
}

// gitlabHeader is the project header's GitLab line: the usage bar and the
// limit line, null when it shows neither.
export function gitlabHeader(
  p: { gitLab?: { rateLimit?: GitLabRateLimitStatus }; gitLabReserve: number } | null | undefined,
  now: number,
): { usage: GitLabUsage | null; limited: string | null } | null {
  const r = p?.gitLab?.rateLimit
  const usage = gitlabUsage(r, p?.gitLabReserve ?? 0, now)
  const limited = gitlabLimitedLine(r, now)
  return usage || limited ? { usage, limited } : null
}

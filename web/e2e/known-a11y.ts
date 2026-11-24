import { assertKnownKeys } from './knownKeys.ts'

// The accessibility checks that fail today, as "<route>@<width>" (light, or
// the route's own scheme) and "<route>@<width>-dark" (the dark pass), see
// desk.spec.ts. Each value names, per requirement of the milestone spec
// (docs/specs/desk-accessible.spec.md), the issue that fixes it and the axe
// rule ids it fixes: "<issue> R<n>: <rule id>, ...; <issue> R<m>: ...".
// The listed rules must be exactly the ones axe finds: a new rule fails the
// check, and so does a listed rule that axe no longer finds. The check runs
// as test.fail(), so Playwright fails the run once a listed check passes: a
// slice that fixes a rule removes it here, and the entry once it is empty.

// The issue of each requirement that has entries here.
const issue = (n: number) => `https://gitlab.com/jo.wiemers/ai-operator/-/issues/${n}`
// color-contrast and target-size, which no requirement covers: R6 (#641)
// fixed its own rules and left these until they have an issue of their own
// (steering log, 2026-11-20).
const R6 = issue(641)

export const knownA11y: Record<string, string> = {
  'overview@375': `${R6} R6: color-contrast`,
  'overview@1180': `${R6} R6: color-contrast`,
  'overview@1180-dark': `${R6} R6: color-contrast`,
  'overview-dark@375': `${R6} R6: color-contrast`,
  'overview-dark@1180': `${R6} R6: color-contrast`,
  'overview-activity@375': `${R6} R6: color-contrast`,
  'overview-activity@1180': `${R6} R6: color-contrast`,
  'overview-activity@1180-dark': `${R6} R6: color-contrast`,
  'inbox@375': `${R6} R6: color-contrast`,
  'inbox@1280-dark': `${R6} R6: color-contrast`,
  'metrics@375': `${R6} R6: color-contrast`,
  'metrics-dark@375': `${R6} R6: color-contrast`,
  'metrics-filtered@375': `${R6} R6: color-contrast`,
  'stream-project-activity@375': `${R6} R6: color-contrast`,
  'stream-project-activity@1180': `${R6} R6: color-contrast, target-size`,
  'stream-project-activity@1180-dark': `${R6} R6: color-contrast, target-size`,
  'stream-project-decisions@375': `${R6} R6: color-contrast`,
  'stream-project-decisions@1180': `${R6} R6: color-contrast`,
  'stream-project-decisions@1180-dark': `${R6} R6: color-contrast`,
  'stream-project-board@375': `${R6} R6: color-contrast`,
  'stream-project-board@1180': `${R6} R6: color-contrast`,
  'stream-project-board@1180-dark': `${R6} R6: color-contrast`,
  'stream-project-metrics@1180-dark': `${R6} R6: color-contrast`,
  'stream-project-dark@375': `${R6} R6: color-contrast`,
  'stream-project-dark@1180': `${R6} R6: color-contrast`,
  'stream-milestone-issues@375': `${R6} R6: color-contrast, target-size`,
  'stream-milestone-issues@1180-dark': `${R6} R6: color-contrast`,
  'stream-milestone-lanes@375': `${R6} R6: color-contrast, target-size`,
  'stream-milestone-lanes@1180-dark': `${R6} R6: color-contrast`,
  'stream-milestone-needs@375': `${R6} R6: color-contrast`,
  'stream-milestone-needs@1180': `${R6} R6: color-contrast`,
  'stream-milestone-needs@1180-dark': `${R6} R6: color-contrast`,
  'stream-milestone-decisions@375': `${R6} R6: color-contrast`,
  'stream-milestone-decisions@1180': `${R6} R6: color-contrast`,
  'stream-milestone-decisions@1180-dark': `${R6} R6: color-contrast`,
  'stream-milestone-retrospective@375': `${R6} R6: color-contrast`,
  'stream-milestone-retrospective@1180': `${R6} R6: color-contrast`,
  'stream-milestone-retrospective@1180-dark': `${R6} R6: color-contrast`,
  'stream-milestone-activity@375': `${R6} R6: color-contrast`,
  'stream-milestone-activity@1180': `${R6} R6: color-contrast`,
  'stream-milestone-activity@1180-dark': `${R6} R6: color-contrast`,
  'stream-milestone-board@375': `${R6} R6: color-contrast`,
  'stream-milestone-board@1180': `${R6} R6: color-contrast`,
  'stream-milestone-board@1180-dark': `${R6} R6: color-contrast`,
  'stream-milestone-metrics@1180-dark': `${R6} R6: color-contrast`,
  'issue@375': `${R6} R6: color-contrast`,
  'issue@1180-dark': `${R6} R6: color-contrast`,
  'issue-chat@375': `${R6} R6: color-contrast`,
  'issue-chat@1180-dark': `${R6} R6: color-contrast`,
  'issue-live@375': `${R6} R6: color-contrast`,
  'issue-logs@375': `${R6} R6: color-contrast`,
  'issue-calls@375': `${R6} R6: color-contrast`,
  'task@1180-dark': `${R6} R6: color-contrast`,
  'task-chat@375': `${R6} R6: color-contrast`,
  'task-chat@1180-dark': `${R6} R6: color-contrast`,
  'task-live@375': `${R6} R6: color-contrast`,
  'task-live@1180': `${R6} R6: color-contrast`,
  'task-live@1180-dark': `${R6} R6: color-contrast`,
  'task-logs@1180-dark': `${R6} R6: color-contrast`,
  'task-calls@1180-dark': `${R6} R6: color-contrast`,
  'stream-project-plans@1180-dark': `${R6} R6: color-contrast`,
  'all-plans@1280-dark': `${R6} R6: color-contrast`,
  'plan-new@1280-dark': `${R6} R6: color-contrast`,
  'plan-new-dark@375': `${R6} R6: color-contrast`,
  'plan-new-dark@1280': `${R6} R6: color-contrast`,
  'plan-waiting@1280-dark': `${R6} R6: color-contrast`,
  'researches@375': `${R6} R6: color-contrast`,
  'researches@1280': `${R6} R6: color-contrast`,
  'researches@1280-dark': `${R6} R6: color-contrast`,
  'research-new@375': `${R6} R6: color-contrast`,
  'research-new@1280-dark': `${R6} R6: color-contrast`,
  'research@375': `${R6} R6: color-contrast`,
  'research@1280': `${R6} R6: color-contrast`,
  'research@1280-dark': `${R6} R6: color-contrast`,
  'research-report@375': `${R6} R6: color-contrast`,
  'research-report@1280-dark': `${R6} R6: color-contrast`,
  'research-runs@375': `${R6} R6: color-contrast`,
  'research-runs@1280-dark': `${R6} R6: color-contrast`,
  'research-run@375': `${R6} R6: color-contrast`,
  'research-run@1280-dark': `${R6} R6: color-contrast`,
}

assertKnownKeys('known-a11y.ts', knownA11y, { dark: true, rules: true })

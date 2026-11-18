// The #558 incident (%29) replayed on milestone 14: run 1 of #558 did the
// work and then failed to decode its answer; the run's 348 lines were saved
// as a WIP commit, MR !583 is open and #559 and #560 wait on #558. The
// milestone is paused on #558, #558 is held with the generic needs-input
// text of an older release, and two pilot prompts ask about it.
import type { MilestoneView, PromptView } from '../../src/types'
import { ago } from './clock'
import { paymentsMilestone } from './stream'

const gitlab = 'https://gitlab.example.com/team-a/shop'
const prompt = paymentsMilestone.prompts!.find((p) => p.id === 'Q3')!

const assess = (id: string): PromptView => ({
  ...prompt,
  id,
  kind: 'assessment',
  issueIID: 558,
  question: `${id}: rerun #558 with a hint to answer in JSON?`,
  state: 'open',
  answer: undefined,
  closedAt: undefined,
  closedReason: undefined,
  closedBy: undefined,
  canAnswer: true,
})

export const incidentError = 'decoding implementer answer: no JSON object in agent result'

export const incident558: MilestoneView = {
  ...paymentsMilestone,
  issues: [
    ...paymentsMilestone.issues,
    ...[558, 559, 560].map((iid) => ({ ...paymentsMilestone.issues[2]!, iid, title: `Desk facts part ${iid - 557}`, mergeRequestIID: undefined, mergeRequestURL: undefined })),
  ],
  autopilot: {
    ...paymentsMilestone.autopilot!,
    phase: 'Paused',
    pauseKind: 'stuck',
    pausedFor: undefined,
    pauseIssue: 558,
    pausedReason: 'stuck: #560 waits for another pending issue',
    holds: [
      {
        ...paymentsMilestone.autopilot!.holds![0]!,
        iid: 558,
        title: 'Desk facts part 1',
        kind: 'needsInput',
        reason: '#558 needs input (see its last comments): CI is still red, the run failed or the agent asked a question',
      },
    ],
  },
  pilot: { ...paymentsMilestone.pilot!, proposals: [] },
  prompts: [assess('Q1'), assess('Q2')],
  problems: [
    {
      iid: 558,
      issue: { iid: 558, title: 'Desk facts part 1', url: `${gitlab}/-/issues/558` },
      mergeRequest: {
        iid: 583,
        url: `${gitlab}/-/merge_requests/583`,
        headSHA: '80cd646a1b2c3d4e5f60718293a4b5c6d7e8f901',
        commitsAhead: 1,
        pushedSHA: '80cd646a1b2c3d4e5f60718293a4b5c6d7e8f901',
        pushedAt: ago(20),
      },
      run: {
        task: 'shop-m14-558',
        number: 1,
        result: 'Failed',
        error: incidentError,
        cause: `the run failed: ${incidentError}`,
        noteURL: `${gitlab}/-/issues/558#note_4263`,
      },
      saved: { sha: '80cd646a1b2c3d4e5f60718293a4b5c6d7e8f901', lines: 348 },
      blocked: [559, 560],
    },
  ],
}

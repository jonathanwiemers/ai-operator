// The project Cockpit's fixture (R15 of "Desk: the Cockpit", #790): the
// project team-a/shop with two running milestones that both need you. %14
// (paused) has its problems and prompts; %12 (running) gets one open pilot
// proposal here, so the project Cockpit pins cards of both. The project's
// stream (answerStream) already has folded runs of #281 (%14) and #277 (%12).
import type { MilestoneView } from '../../src/types'
import { ago } from './clock'
import { milestones } from './stream'

export const checkoutWithProposal: MilestoneView = {
  ...milestones[12]!,
  pilot: {
    ...milestones[12]!.pilot!,
    proposals: [{ id: 4, summary: 'Split #278 into the mail template and the sending job.', postedAt: ago(15) }],
  },
}

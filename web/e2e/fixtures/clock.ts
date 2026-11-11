import type { Cost } from '../../src/types'

// now is the browser's fixed time in the checks (page.clock.setFixedTime),
// so relative times in the screenshots stay the same from run to run.
export const now = new Date('2026-11-10T12:00:00Z')

// ago is now minus the given minutes, as the API sends times.
export function ago(minutes: number): string {
  return new Date(now.getTime() - minutes * 60_000).toISOString()
}

// cost is a cost from agent calls.
export function cost(usd: string, tokens: number): Cost {
  return { costUSD: usd, costKnown: true, tokens, costFrom: 'calls' }
}

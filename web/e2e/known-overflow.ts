import { assertKnownKeys } from './knownKeys.ts'

// The checks that overflow sideways today, as "<route>@<width>". They run as
// test.fail(): Playwright fails the run when one of them passes, so a slice
// that fixes an overflow removes its entry here. Each entry names the issue
// that fixes it, what is too wide and by how much (scrollWidth - clientWidth,
// measured).
export const knownOverflow: Record<string, string> = {}

assertKnownKeys('known-overflow.ts', knownOverflow, { dark: false })

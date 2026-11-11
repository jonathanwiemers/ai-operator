// The checks that overflow sideways today, as "<route>@<width>". They run as
// test.fail(): Playwright fails the run when one of them passes, so a slice
// that fixes an overflow removes its entry here. Each entry names what is
// too wide and by how much (scrollWidth - clientWidth, measured).
export const knownOverflow: Record<string, string> = {}

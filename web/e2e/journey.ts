// The journey set: the critical user journeys that every merge request runs
// (`npm run e2e:journey`, `--grep @journey`). The full set runs nightly and on
// merge requests that change the checks. A test joins when its failure would
// block the owner's daily loop, not for coverage; keep the set at 25 tests at
// most (docs/specs/ci-time-reduction.spec.md, §5.5). Only the 1280 width
// joins, the other widths stay in the full set.
export const journeyAt = (width: number) => ({ tag: width === 1280 ? ['@journey'] : [] })

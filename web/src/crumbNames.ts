import { reactive } from 'vue'
import type { CrumbNames } from './crumbs.ts'

// crumbNames holds the titles the breadcrumbs show (decision 17): views
// put in what they already loaded, no extra request. Each entry names
// what it belongs to, so a stale one is ignored on other routes.
export const crumbNames = reactive<CrumbNames>({})

// setCrumbNames merges names into the store; an entry given as undefined
// stays as it was.
export function setCrumbNames(names: CrumbNames): void {
  for (const [k, v] of Object.entries(names) as [keyof CrumbNames, CrumbNames[keyof CrumbNames]][]) {
    if (v) Object.assign(crumbNames, { [k]: v })
  }
}

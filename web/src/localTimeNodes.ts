import { h, type VNodeChild } from 'vue'
import { localTimes } from './localTime.ts'

// Apart from localTime.ts, which stays free of Vue: the lazy deskWorker.ts
// uses it for notification bodies, and Vue reached from there would split
// the shared chunks (docs/contributing/web-ui.md, "Bundle budget").

// localTimeNodes renders text with each UTC time as a <time> element: the
// local label, the instant in datetime and the full times in its title.
export function localTimeNodes(text: string, now: number): VNodeChild[] {
  return localTimes(text, now).map((p) => ('text' in p ? p.text : h('time', { datetime: p.iso, title: p.title }, p.label)))
}

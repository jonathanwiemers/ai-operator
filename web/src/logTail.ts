// maxFollowLines is how many lines the followed log keeps (D496-P1): a
// phone must not grow memory without bound; Download covers more.
export const maxFollowLines = 2000

// appendLog appends chunk to text and keeps the last max lines; an
// unfinished last line counts as one.
export function appendLog(text: string, chunk: string, max = maxFollowLines): string {
  const all = text + chunk
  const lines = all.split('\n')
  const extra = lines.length - (lines.at(-1) === '' ? max + 1 : max)
  return extra > 0 ? lines.slice(extra).join('\n') : all
}

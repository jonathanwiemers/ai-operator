// milestoneName is how a list names a milestone (Decision 11 of "Desk: one
// look on every page"): its title, with %n as a secondary label; %n alone
// (ref '') when the title is not known. A blank title counts as unknown.
export function milestoneName(iid: number | string, title?: string | null): { title: string; ref: string } {
  const t = title?.trim()
  return t ? { title: t, ref: `%${iid}` } : { title: `%${iid}`, ref: '' }
}

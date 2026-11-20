// The keys and values of the known-failure maps of the browser checks
// (known-overflow.ts, known-a11y.ts). A key is the check's id,
// "<route>@<width>"; the a11y map also has "<route>@<width>-dark" for the
// dark pass. A value names the issue that fixes the entry, as a link
// (".../issues/<n>") or "#<n>"; an a11y value also lists the axe rule ids per
// requirement, "R<n>: <rule id>, ...". Checked by test/knownKeys.test.mjs and
// when the maps load.

// overflowKey is a key of known-overflow.ts.
export const overflowKey = /^[a-z0-9-]+@\d+$/

// a11yKey is a key of known-a11y.ts.
export const a11yKey = /^[a-z0-9-]+@\d+(-dark)?$/

const issueLink = /\/issues\/\d+|#\d+/

// knownRules returns the axe rule ids an a11y value lists after "R<n>: ".
export function knownRules(value: string): string[] {
  return [...value.matchAll(/\bR\d+: ([a-z0-9-]+(?:, [a-z0-9-]+)*)/g)].flatMap((m) => m[1]!.split(', '))
}

// knownKeyProblems lists what is wrong with a known map, one line per entry:
// a key that does not match the pattern (dark: whether "-dark" is allowed),
// a value without an issue link, or (rules) a value without rule ids.
// Empty when the map is fine.
export function knownKeyProblems(map: Record<string, string>, { dark, rules = false }: { dark: boolean; rules?: boolean }): string[] {
  const key = dark ? a11yKey : overflowKey
  const problems: string[] = []
  for (const [k, v] of Object.entries(map)) {
    if (!key.test(k)) problems.push(`${k}: key is not "<route>@<width>${dark ? '[-dark]' : ''}"`)
    if (!issueLink.test(v)) problems.push(`${k}: value has no issue link (".../issues/<n>" or "#<n>")`)
    if (rules && knownRules(v).length === 0) problems.push(`${k}: value lists no axe rule ids ("R<n>: <rule id>, ...")`)
  }
  return problems
}

// assertKnownKeys throws on the problems of knownKeyProblems, so a bad entry
// stops the browser checks before they run.
export function assertKnownKeys(name: string, map: Record<string, string>, opts: { dark: boolean; rules?: boolean }): void {
  const problems = knownKeyProblems(map, opts)
  if (problems.length > 0) throw new Error(`${name}:\n${problems.join('\n')}`)
}

// Input types a phone shows no keyboard for.
const noKeyboard = new Set(['button', 'checkbox', 'color', 'file', 'hidden', 'image', 'radio', 'range', 'reset', 'submit'])

// textEntry tells whether an element takes typed text, so that a phone shows
// its keyboard while it has the focus: an editable textarea or element, or an
// input of a text kind. App.vue hides the bottom bar meanwhile.
export function textEntry(el: unknown): boolean {
  if (!el || typeof el !== 'object') return false
  const e = el as { tagName?: string; type?: string; readOnly?: boolean; isContentEditable?: boolean }
  if (e.isContentEditable) return true
  if (e.tagName === 'TEXTAREA') return !e.readOnly
  if (e.tagName === 'INPUT') return !e.readOnly && !noKeyboard.has(e.type ?? 'text')
  return false
}

// useKeyboardInset keeps --keyboard-inset on <html> at the height the
// on-screen keyboard covers at the bottom of the layout viewport, so a
// composer that is sticky at the bottom stays above it (docs/ui.md,
// "Composer"). iOS Safari does not resize the layout viewport for the
// keyboard, only the visual one; Chrome on Android does
// (interactive-widget=resizes-content in index.html), so there the inset
// stays 0. The listeners run while at least one component uses it.
import { onBeforeUnmount, onMounted } from 'vue'

// keyboardInset is how far the visual viewport's bottom edge sits above
// the layout viewport's, in whole px, never below 0.
export function keyboardInset(layoutHeight: number, vv: { height: number; offsetTop: number } | null): number {
  if (!vv) return 0
  return Math.max(0, Math.round(layoutHeight - vv.height - vv.offsetTop))
}

let users = 0

function update(): void {
  const px = keyboardInset(window.innerHeight, window.visualViewport)
  document.documentElement.style.setProperty('--keyboard-inset', `${px}px`)
}

export function useKeyboardInset(): void {
  onMounted(() => {
    const vv = window.visualViewport
    if (!vv) return
    if (users++ === 0) {
      vv.addEventListener('resize', update)
      vv.addEventListener('scroll', update)
      update()
    }
  })
  onBeforeUnmount(() => {
    const vv = window.visualViewport
    if (!vv) return
    if (--users === 0) {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
      document.documentElement.style.removeProperty('--keyboard-inset')
    }
  })
}

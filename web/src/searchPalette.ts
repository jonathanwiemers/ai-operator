// The ⌘K palette's state, shared by the shell (shortcut) and its buttons.
// The palette itself (components/SearchPalette.vue) loads on first open;
// keep imports out of here, this module is in the initial bundle.
import { ref } from 'vue'

export const paletteOpen = ref(false)

// paletteHint: the shortcut as this platform writes it.
const nav = navigator as Navigator & { userAgentData?: { platform?: string } }
export const paletteHint = /mac|iphone|ipad/i.test(nav.userAgentData?.platform ?? navigator.platform) ? '⌘K' : 'Ctrl+K'

// isPaletteKey: ⌘K on macOS, Ctrl+K elsewhere, also inside text fields.
export function isPaletteKey(e: KeyboardEvent): boolean {
  return (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k'
}

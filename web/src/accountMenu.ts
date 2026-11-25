import type { DropdownMenuItem } from '@nuxt/ui'
import type { Theme } from './colorScheme.ts'
import type { Me } from './generated/views'

// The account menu's items (RailNav.vue, docs/contributing/web-ui.md
// "Navigation"), built here so node can test them. Groups, divided by a
// line: who is signed in (slot 'who'), Appearance with Light / Dark / Auto
// (slot 'theme'), Notifications (the dialog), the Desk version (slot
// 'about'), and Sign out with OIDC.

export interface AccountInput {
  me: Pick<Me, 'username' | 'roles' | 'authMode'> | null
  // loginLasts' line, null without OIDC or an expiry.
  lasts: string | null
  // aboutLine's result, null before /about answers.
  about: { text: string; href: string; note: string | null } | null
  theme: Theme
  setTheme: (t: Theme) => void
  // Opens the Notifications dialog (notify.ts, openNotifyDialog).
  openNotifications: () => void
  signOut: () => void
}

// AccountItem: a menu item plus the fields RailNav's slots render; the
// slot names are literal, so the menu's #who, #theme-label and #about-label
// slots are typed.
interface Plain extends DropdownMenuItem {
  slot?: undefined
}
interface Who extends DropdownMenuItem {
  slot: 'who'
  roles: string
  lasts: string | null
}
interface ThemeChoice extends DropdownMenuItem {
  slot: 'theme'
  theme: Theme
}
interface Version extends DropdownMenuItem {
  slot: 'about'
}
export type AccountItem = Plain | Who | ThemeChoice | Version

const themeChoices: { theme: Theme; label: string; icon: string }[] = [
  { theme: 'light', label: 'Light', icon: 'i-lucide-sun' },
  { theme: 'dark', label: 'Dark', icon: 'i-lucide-moon' },
  { theme: 'auto', label: 'Auto', icon: 'i-lucide-monitor' },
]

export function accountItems(input: AccountInput): AccountItem[][] {
  const oidc = input.me?.authMode === 'oidc'
  const who: Who = {
    type: 'label',
    slot: 'who',
    label: oidc ? (input.me?.username ?? '') : 'No login (auth off)',
    roles: (input.me?.roles ?? []).join(', ') || 'no roles',
    lasts: oidc ? input.lasts : null,
  }
  const appearance: AccountItem[] = [
    { type: 'label', label: 'Appearance' },
    ...themeChoices.map(
      (c): ThemeChoice => ({
        type: 'checkbox',
        slot: 'theme',
        label: c.label,
        icon: c.icon,
        theme: c.theme,
        checked: input.theme === c.theme,
        onUpdateChecked: () => input.setTheme(c.theme),
        // The menu stays open, so the change shows at once.
        onSelect: (e: Event) => e.preventDefault(),
      }),
    ),
  ]
  const notifications: AccountItem = { label: 'Notifications', icon: 'i-lucide-bell', onSelect: () => input.openNotifications() }
  const groups: AccountItem[][] = [[who], appearance, [notifications]]
  const a = input.about
  if (a) {
    const link = a.href ? { to: a.href, target: '_blank' } : {}
    groups.push([{ slot: 'about', label: a.text, description: a.note ?? undefined, ...link }])
  }
  if (oidc) groups.push([{ label: 'Sign out', icon: 'i-lucide-log-out', onSelect: () => input.signOut() }])
  return groups
}

// initials are the avatar's letters: the first letters of the first two
// parts of the name ("jo.wiemers" → "JW"), or its first two letters.
export function initials(name: string | undefined | null): string {
  const parts = (name ?? '').split(/[\s._@-]+/).filter(Boolean)
  const s = parts.length > 1 ? `${parts[0]![0]}${parts[1]![0]}` : (parts[0] ?? '').slice(0, 2)
  return s.toUpperCase() || '?'
}

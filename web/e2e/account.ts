import { expect, type Locator, type Page } from '@playwright/test'

// openAccountMenu opens the account menu at the bottom of the rail
// (RailNav.vue, src/accountMenu.ts), from the sidebar or the phone's rail
// sheet, whichever shows the button, and returns the menu. An open menu
// stays as it is. The menu is portalled to <body>, so look up its items on
// the returned locator, not inside the sheet.
export async function openAccountMenu(page: Page): Promise<Locator> {
  const menu = page.getByRole('menu')
  if (!(await menu.isVisible())) await page.locator('[data-account-button]:visible').click()
  await expect(menu).toBeVisible()
  await menu.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  return menu
}

// themeChoice is the account menu's Light, Dark or Auto item, the menu opened first.
export async function themeChoice(page: Page, name: 'Light' | 'Dark' | 'Auto'): Promise<Locator> {
  return (await openAccountMenu(page)).getByRole('menuitemcheckbox', { name })
}

// pageHeaderUi is the :ui of every view's UPageHeader (decision 4, D626-P2):
// compact padding and the Desk's title size instead of Nuxt UI's landing-page
// look, so all headers match. Views pass it as is, without a wrapper.
// Layout: headline (eyebrow) above; the title with the actions (links slot)
// on its right, wrapping below it when narrow; one meta line (description)
// under them. RouteTabs follow the header directly.
export const pageHeaderUi = {
  root: 'border-0 py-0 mb-3',
  headline: 'mb-1 text-xs font-semibold text-(--text-3)',
  wrapper: 'flex-row flex-wrap items-center justify-between gap-x-3 gap-y-2',
  title: 'min-w-0 text-[22px] leading-tight font-[650] tracking-[-0.01em] text-(--text) [overflow-wrap:anywhere]',
  links: 'flex flex-wrap items-center gap-2',
  description: 'mt-1 text-[13px] text-(--text-2) [overflow-wrap:anywhere]',
}

// toolbarUi is the :ui of the UDashboardToolbar at the top of a tab's content
// (decision 3, D672-P1): the tab's switches and filters on the left (#left),
// search or secondary items on the right (#right). Flat, without the
// dashboard's border and padding, and wrapping on phones. searchToolbarUi
// puts the right side on a row of its own below 760 px, so a search takes
// the full width, last.
export const toolbarUi = {
  root: 'mb-3 min-h-0 flex-wrap gap-x-3 gap-y-2 border-0 px-0 sm:px-0',
  left: 'min-w-0 flex-wrap',
  right: 'min-w-0 flex-wrap',
}
export const searchToolbarUi = { ...toolbarUi, right: `${toolbarUi.right} basis-full tablet:basis-auto` }

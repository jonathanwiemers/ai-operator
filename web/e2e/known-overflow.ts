// The checks that overflow sideways today, as "<route>@<width>". They run as
// test.fail(): Playwright fails the run when one of them passes, so a slice
// that fixes an overflow removes its entry here. Each entry names what is
// too wide and by how much (scrollWidth - clientWidth, measured).
export const knownOverflow: Record<string, string> = {
  // The paused strip (.paused-strip in Inbox.vue) keeps its parts on one
  // line, so the page is at least 741 px wide.
  'inbox@375': 'content pane 366 px: the Inbox paused strip',
  'inbox@768': 'content pane 221 px: the Inbox paused strip',
}

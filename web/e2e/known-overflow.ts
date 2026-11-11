// The checks that overflow sideways today, as "<route>@<width>". They run as
// test.fail(): Playwright fails the run when one of them passes, so a slice
// that fixes an overflow removes its entry here. Each entry names what is
// too wide and by how much (scrollWidth - clientWidth, measured).
export const knownOverflow: Record<string, string> = {
  // TaskBody.vue's .cols use minmax(360px, 1fr): the Review/CI and
  // Pod/Conditions rows need 360 px of the phone's 342.
  'issue@375': 'content pane 34 px: the task body columns (.cols, minmax 360 px)',
  'task@375': 'content pane 33 px: the task body columns (.cols, minmax 360 px)',

  // The one-off form's project select is as wide as its longest option.
  'research-new@375': 'content pane 162 px: the project select',
}

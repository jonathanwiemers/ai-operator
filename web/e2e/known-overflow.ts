// The checks that overflow sideways today, as "<route>@<width>". They run as
// test.fail(): Playwright fails the run when one of them passes, so a slice
// that fixes an overflow removes its entry here. Each entry names what is
// too wide and by how much (scrollWidth - clientWidth, measured).
export const knownOverflow: Record<string, string> = {
  // Stream.vue's .title-row keeps the title, the tab strip and (on the
  // Stream tab) the search form on one line.
  'stream-project-stream@375': 'content pane 192 px: the title row (tab strip and search form)',
  'stream-project-stream@768': 'content pane 119 px: the title row (search form)',
  'stream-project-board@375': 'content pane 48 px: the title row (tab strip)',
  'stream-project-spend@375': 'content pane 48 px: the title row (tab strip); the Spend tables are wider still',
  'stream-milestone-issues@375': 'content pane 338 px: the title row (tab strip)',
  'stream-milestone-issues@768': 'content pane 205 px: the title row (tab strip)',
  'stream-milestone-pilot@375': 'content pane 338 px: the title row (tab strip)',
  'stream-milestone-pilot@768': 'content pane 205 px: the title row (tab strip)',
  'stream-milestone-retrospective@375': 'content pane 338 px: the title row (tab strip)',
  'stream-milestone-retrospective@768': 'content pane 205 px: the title row (tab strip)',
  'stream-milestone-stream@375': 'content pane 482 px: the title row (tab strip and search form)',
  'stream-milestone-stream@768': 'content pane 409 px: the title row (tab strip and search form)',
  'stream-milestone-board@375': 'content pane 338 px: the title row (tab strip)',
  'stream-milestone-board@768': 'content pane 205 px: the title row (tab strip)',
  'stream-milestone-spend@375': 'content pane 338 px: the title row (tab strip); the Spend tables are wider still',
  'stream-milestone-spend@768': 'content pane 205 px: the title row (tab strip) and the Spend tables',

  // TaskBody.vue's .cols use minmax(360px, 1fr): the Review/CI and
  // Pod/Conditions rows need 360 px of the phone's 342.
  'issue@375': 'content pane 34 px: the task body columns (.cols, minmax 360 px)',
  'task@375': 'content pane 33 px: the task body columns (.cols, minmax 360 px)',

  // The one-off form's project select is as wide as its longest option.
  'research-new@375': 'content pane 162 px: the project select',
}

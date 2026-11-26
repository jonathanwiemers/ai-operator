// Tests of the upload link finder and the message split in src/attachments.ts.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { findUploads, splitAttachments, uploadHref, uploadRewrite } from '../src/attachments.ts'

const s = '0123456789abcdef0123456789abcdef'
const s2 = 'fedcba9876543210fedcba9876543210'
const project = { url: 'https://gitlab.example', path: 'group/app' }
const href = (l) => uploadHref('team', 'app', l)

test('findUploads: the three forms of Decision 9', () => {
  const text = [
    `![shot](/uploads/${s}/shot.png)`,
    `[log](https://gitlab.example/group/app/uploads/${s2}/build.log)`,
    `https://gitlab.example/-/project/42/uploads/${s}/doc.pdf`,
  ].join('\n')
  assert.deepEqual(
    findUploads(text, project).map((l) => [l.secret, l.filename, l.image]),
    [
      [s, 'shot.png', true],
      [s2, 'build.log', false],
    ],
  )
  assert.deepEqual(
    findUploads(text, { ...project, id: 42 }).map((l) => l.filename),
    ['shot.png', 'build.log', 'doc.pdf'],
  )
  // Without a project only the relative form counts.
  assert.deepEqual(
    findUploads(text).map((l) => l.filename),
    ['shot.png'],
  )
})

test('findUploads: case-insensitive prefix and secret, decoded names, each once', () => {
  const text = `[a](HTTPS://GITLAB.EXAMPLE/Group/App/uploads/${s.toUpperCase()}/my%20file.txt) [b](/uploads/${s}/my%20file.txt) https://gitlab.example/group/app/uploads/${s2}/x.log.`
  assert.deepEqual(
    findUploads(text, project).map((l) => [l.secret, l.filename, l.raw]),
    [
      [s, 'my file.txt', `HTTPS://GITLAB.EXAMPLE/Group/App/uploads/${s.toUpperCase()}/my%20file.txt`],
      [s2, 'x.log', `https://gitlab.example/group/app/uploads/${s2}/x.log`],
    ],
  )
})

test('findUploads: other projects, hosts, bad secrets and names are ignored', () => {
  const bad = [
    `[a](https://gitlab.example/group/other/uploads/${s}/a.png)`,
    `[a](https://evil.example/group/app/uploads/${s}/a.png)`,
    `[a](https://gitlab.example/-/project/7/uploads/${s}/a.png)`,
    `[a](/uploads/${s.slice(1)}/a.png)`,
    `[a](/uploads/${s.slice(0, 31)}g/a.png)`,
    `[a](/uploads/${s}/../a.png)`,
    `[a](/uploads/${s}/%2e%2e)`,
    `[a](/uploads/${s}/a%2Fb.png)`,
    `[a](/uploads/${s}/a%5Cb.png)`,
    `[a](/uploads/${s}/a.png?x=1)`,
    `[a](/uploads/${s}/%E0%A4%A)`,
    `/uploads/${s}/bare.png`,
  ]
  for (const t of bad) assert.deepEqual(findUploads(t, { ...project, id: 42 }), [], t)
})

test('splitAttachments: link lines go below the text', () => {
  const body = `the header overlaps the tabs\n\nsee below\n![shot.png](/uploads/${s}/shot.png)\n[build.log](/uploads/${s2}/build.log)`
  assert.deepEqual(splitAttachments(body, project, href), {
    text: 'the header overlaps the tabs\n\nsee below',
    files: [
      { name: 'shot.png', href: `/api/v1/projects/team/app/plans/uploads/${s}/shot.png`, image: true },
      { name: 'build.log', href: `/api/v1/projects/team/app/plans/uploads/${s2}/build.log`, image: false },
    ],
  })
})

test('splitAttachments: mid-sentence links, duplicates and images by extension', () => {
  const body = `look at [this](/uploads/${s}/a%20b.PNG) and https://gitlab.example/group/app/uploads/${s}/a%20b.PNG, ok`
  const r = splitAttachments(body, project, href)
  assert.equal(r.text, 'look at and, ok')
  assert.deepEqual(r.files, [{ name: 'a b.PNG', href: `/api/v1/projects/team/app/plans/uploads/${s}/a%20b.PNG`, image: true }])
})

test('splitAttachments: a body of links only, and other links kept as text', () => {
  assert.equal(splitAttachments(`![a](/uploads/${s}/a.png)\n![b](/uploads/${s2}/b.gif)`, project, href).text, '')
  const other = `[x](https://gitlab.example/group/other/uploads/${s}/x.png)\n[y](https://a.example)`
  assert.deepEqual(splitAttachments(other, project, href), { text: other, files: [] })
  const pdf = splitAttachments(`![doc](/uploads/${s}/doc.pdf)`, project, href)
  assert.equal(pdf.files[0].image, false)
})

test('uploadHref encodes the parts', () => {
  assert.equal(uploadHref('a b', 'c/d', { secret: s, filename: 'x#y?.png' }), `/api/v1/projects/a%20b/c%2Fd/plans/uploads/${s}/x%23y%3F.png`)
})

test('uploadRewrite maps the plan project uploads only', () => {
  const r = uploadRewrite({ namespace: 'team', name: 'app', project, connected: true })
  assert.equal(r(`/uploads/${s}/a.png`), `/api/v1/projects/team/app/plans/uploads/${s}/a.png`)
  assert.equal(r(`https://gitlab.example/group/app/uploads/${s}/a%20b.log`), `/api/v1/projects/team/app/plans/uploads/${s}/a%20b.log`)
  assert.equal(r(`https://gitlab.example/group/other/uploads/${s}/a.png`), undefined)
  assert.equal(r('https://a.example/x'), undefined)
})

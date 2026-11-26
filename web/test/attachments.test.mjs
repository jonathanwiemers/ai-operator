import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  accept,
  allowedExtensions,
  bodyTooLong,
  checkFiles,
  fileSize,
  maxFileBytes,
  maxFiles,
  pastedFiles,
  pastedName,
  uploadAll,
  withAttachments,
} from '../src/attachments.ts'

// file is a File of the given size without allocating it all for big ones.
function file(name, size = 10, type = '') {
  const f = new File(['x'.repeat(Math.min(size, 16))], name, { type })
  return size > 16 ? Object.defineProperty(f, 'size', { value: size }) : f
}

test('the limits and the accept filter', () => {
  assert.equal(maxFileBytes, 10 << 20)
  assert.equal(maxFiles, 5)
  assert.equal(accept, allowedExtensions.join(','))
  assert.ok(accept.startsWith('.png,.jpg,.jpeg,'))
})

test('checkFiles takes allowed types in any case and keeps the order', () => {
  const a = file('a.txt')
  const { files, refused } = checkFiles([a], [file('Shot.PNG'), file('build.log'), file('doc.pdf'), file('conf.YML')])
  assert.deepEqual(
    files.map((f) => f.name),
    ['a.txt', 'Shot.PNG', 'build.log', 'doc.pdf', 'conf.YML'],
  )
  assert.deepEqual(refused, [])
})

test('checkFiles refuses with the file and the rule', () => {
  const current = [file('f1.txt'), file('f2.txt'), file('f3.txt'), file('f4.txt')]
  const { files, refused } = checkFiles(current, [
    file('x.zip'),
    file('noext'),
    file('big.png', 12 * 1024 * 1024),
    file('edge.png', maxFileBytes),
    file('empty.txt', 0),
    file('f6.txt'),
  ])
  assert.deepEqual(
    files.map((f) => f.name),
    ['f1.txt', 'f2.txt', 'f3.txt', 'f4.txt', 'edge.png'],
  )
  assert.deepEqual(
    refused.map((r) => r.reason),
    [
      'x.zip: only PNG, JPG, GIF, WebP, PDF and text files',
      'noext: only PNG, JPG, GIF, WebP, PDF and text files',
      'big.png: larger than 10 MiB',
      'empty.txt: the file is empty',
      'f6.txt: at most 5 files per message',
    ],
  )
  assert.equal(refused[0].name, 'x.zip')
})

test('pastedName and pastedFiles name clipboard screenshots', () => {
  assert.equal(pastedName(1), 'pasted-1.png')
  const named = file('diagram.png', 10, 'image/png')
  const out = pastedFiles([file('', 10, 'image/png'), named, file('image.png', 10, 'image/png'), file('notes.txt', 10, 'text/plain')], 3)
  assert.deepEqual(
    out.map((f) => f.name),
    ['pasted-3.png', 'diagram.png', 'pasted-4.png', 'notes.txt'],
  )
  assert.equal(out[1], named)
  assert.equal(out[0].type, 'image/png')
})

test('fileSize', () => {
  assert.equal(fileSize(512), '512 B')
  assert.equal(fileSize(2048), '2 KiB')
  assert.equal(fileSize(3.5 * 1024 * 1024), '3.5 MiB')
})

test('withAttachments puts one line per file after the text', () => {
  const md = ['![a.png](/uploads/0123/a.png)', '[b.log](/uploads/4567/b.log)']
  assert.equal(withAttachments('  see this \n', md), 'see this\n![a.png](/uploads/0123/a.png)\n[b.log](/uploads/4567/b.log)')
  assert.equal(withAttachments('   ', md), md.join('\n'))
  assert.equal(withAttachments('only text', []), 'only text')
})

test('bodyTooLong counts code points like the server', () => {
  assert.equal(bodyTooLong('a'.repeat(16384)), false)
  assert.equal(bodyTooLong('a'.repeat(16385)), true)
  assert.equal(bodyTooLong('😀'.repeat(16384)), false)
})

test('uploadAll uploads in order and returns the markdown', async () => {
  const seen = []
  const res = await uploadAll([file('a.png'), file('b.log')], async (f) => {
    seen.push(f.name)
    return { markdown: `[${f.name}](/uploads/x/${f.name})` }
  })
  assert.deepEqual(seen, ['a.png', 'b.log'])
  assert.deepEqual(res, { markdown: ['[a.png](/uploads/x/a.png)', '[b.log](/uploads/x/b.log)'] })
})

class Status extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

test('uploadAll stops at the first failure and names the file', async () => {
  const seen = []
  const res = await uploadAll([file('a.png'), file('b.png'), file('c.png')], async (f) => {
    seen.push(f.name)
    if (f.name === 'b.png') throw new Status('the content is not a PNG image', 400)
    return { markdown: 'x' }
  })
  assert.deepEqual(seen, ['a.png', 'b.png'])
  assert.deepEqual(res, { error: 'b.png: the content is not a PNG image', file: 'b.png', status: 400 })
})

test('uploadAll keeps a server message that names the file', async () => {
  const res = await uploadAll([file('big.png')], async () => {
    throw new Status('big.png: larger than 10 MiB', 413)
  })
  assert.equal(res.error, 'big.png: larger than 10 MiB')
})

test('uploadAll asks to connect GitLab on 409', async () => {
  const res = await uploadAll([file('a.png')], async () => {
    throw new Status('connect your GitLab account first', 409)
  })
  assert.deepEqual(res, { error: 'Connect your GitLab account to attach files.', file: 'a.png', status: 409 })
})

test('uploadAll: 429 and errors without a status', async () => {
  const limited = await uploadAll([file('a.png')], async () => {
    throw new Status('slow down', 429)
  })
  assert.equal(limited.error, 'a.png: too many uploads; try again in a minute.')
  const offline = await uploadAll([file('a.png')], async () => {
    throw new TypeError('Failed to fetch')
  })
  assert.deepEqual(offline, { error: 'a.png: Failed to fetch', file: 'a.png' })
})

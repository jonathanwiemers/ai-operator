// Tests of src/about.ts: npm test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { aboutLine, compareVersions } from '../src/about.ts'

const base = { version: '2026.11.9', releasesURL: 'https://gl/releases', release: null }
const rel = (version, state, url = '') => ({ ...base, release: { version, state, url } })

test('compareVersions is numeric and ignores dev', () => {
  assert.equal(compareVersions('2026.11.10', '2026.11.9'), 1)
  assert.equal(compareVersions('2026.11.9', '2026.12.1'), -1)
  assert.equal(compareVersions('2026.11.9', '2026.11.9'), 0)
  assert.equal(compareVersions('dev', '2026.11.9'), 0)
  assert.equal(compareVersions('2026.11.9', 'dev'), 0)
})

test('link: release url, calendar fallback, dev, no releasesURL', () => {
  assert.equal(aboutLine(rel('2026.11.9', 'Released', 'https://gl/r/9')).href, 'https://gl/r/9')
  assert.equal(aboutLine(base).href, 'https://gl/releases/2026.11.9')
  assert.equal(aboutLine({ ...base, version: 'dev' }).href, 'https://gl/releases')
  assert.equal(aboutLine({ ...base, releasesURL: '' }).href, '')
  assert.equal(aboutLine(base).text, 'ai-operator 2026.11.9')
})

test('notes', () => {
  assert.equal(aboutLine(rel('2026.11.10', 'Waiting')).note, 'Update to 2026.11.10 rolling out')
  assert.equal(aboutLine(rel('2026.11.10', 'Running')).note, 'Update to 2026.11.10 rolling out')
  assert.equal(aboutLine(rel('2026.11.10', 'Released')).note, '2026.11.10 is out — reload')
  assert.equal(aboutLine(rel('2026.11.10', 'Failed')).note, null)
  assert.equal(aboutLine(rel('2026.11.9', 'Released')).note, null)
  assert.equal(aboutLine(rel('2026.11.8', 'Released')).note, null)
  assert.equal(aboutLine({ ...rel('2026.11.10', 'Running'), version: 'dev' }).note, null)
})

// The browser baked into Dockerfile.agent-web must match the Playwright the
// web UI pins: npm test.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

// pinnedVersion returns the value of the Dockerfile's ARG PLAYWRIGHT_VERSION=
// line, or undefined when there is none.
function pinnedVersion(dockerfile) {
  const m = /^ARG PLAYWRIGHT_VERSION=(\S+)\s*$/m.exec(dockerfile)
  return m ? m[1] : undefined
}

function checkPin(dockerVersion, packageVersion) {
  assert.equal(
    dockerVersion,
    packageVersion,
    `Dockerfile.agent-web pins PLAYWRIGHT_VERSION=${dockerVersion} but web/package.json has @playwright/test ${packageVersion}; bump both`,
  )
}

test('pinnedVersion finds the ARG line', () => {
  assert.equal(pinnedVersion('FROM x\nARG PLAYWRIGHT_VERSION=1.2.3\nRUN y\n'), '1.2.3')
})

test('pinnedVersion is undefined without the ARG line', () => {
  assert.equal(pinnedVersion('FROM x\nARG OTHER=1\n'), undefined)
  assert.equal(pinnedVersion('# ARG PLAYWRIGHT_VERSION=1.0.0\n'), undefined)
})

test('checkPin names both versions when they differ', () => {
  assert.throws(() => checkPin('1.64.0', '1.65.0'), (err) => {
    assert.match(err.message, /PLAYWRIGHT_VERSION=1\.64\.0/)
    assert.match(err.message, /@playwright\/test 1\.65\.0/)
    return true
  })
  checkPin('1.65.0', '1.65.0')
})

test('Dockerfile.agent-web pins the Playwright of web/package.json', () => {
  const dockerfile = readFileSync(new URL('../../Dockerfile.agent-web', import.meta.url), 'utf8')
  const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'))
  checkPin(pinnedVersion(dockerfile), pkg.devDependencies['@playwright/test'])
})

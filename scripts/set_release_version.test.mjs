import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, copyFile, readFile, rm } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { packageVersion, synchronizeVersion, versionFiles } from './set_release_version.mjs'

test('timestamp mapping preserves valid npm versions and chronological ordering', () => {
  assert.equal(packageVersion('v20261117-075121'), '20261117.75121.0')
  assert.equal(packageVersion('v20260101-000000'), '20260101.0.0')
  assert.equal(packageVersion('v20240229-235959'), '20240229.235959.0')
  for (const tag of ['0.3.1', 'dsh-v0.3.1', 'v20260229-000000', 'v20261301-000000', 'v20261117-240000', 'v20261117-075160']) assert.throws(() => packageVersion(tag))
})

test('one release updates all version fields, checks drift, and is idempotent', async t => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'ocv-version-')), root = pathToFileURL(directory + path.sep)
  t.after(() => rm(directory, { recursive: true, force: true }))
  for (const file of versionFiles) {
    const target = new URL(file, root)
    await mkdir(path.dirname(fileURLToPath(target)), { recursive: true })
    await copyFile(new URL('../' + file, import.meta.url), target)
  }
  const tag = 'v20260201-001203'
  await assert.rejects(synchronizeVersion(root, tag, true), /version mismatch/)
  await synchronizeVersion(root, tag)
  assert.equal((await synchronizeVersion(root, tag, true)).packageVersion, '20260201.1203.0')
  assert.deepEqual((await synchronizeVersion(root, tag)).changed, [])
  assert.match(await readFile(new URL('skills/dsh-service.mjs', root), 'utf8'), /VERSION = 'v20260201-001203'/)
  assert.equal(JSON.parse(await readFile(new URL('package.json', root), 'utf8')).version, '20260201.1203.0')
  const before = await readFile(new URL('package.json', root), 'utf8')
  await assert.rejects(synchronizeVersion(root, 'v20260230-000000'))
  assert.equal(await readFile(new URL('package.json', root), 'utf8'), before)
})

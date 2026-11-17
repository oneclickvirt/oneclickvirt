import assert from 'node:assert/strict'
import { readFile, access } from 'node:fs/promises'
import { spawnSync } from 'node:child_process'
import { VERSION } from '../skills/dsh-service.mjs'
const pkg = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'))
const lock = JSON.parse(await readFile(new URL('../package-lock.json', import.meta.url), 'utf8'))
assert.equal(pkg.name, 'dsh-plugin-oneclickvirt'); assert.equal(pkg.version, VERSION)
assert.equal(lock.version, VERSION); assert.equal(pkg.license, 'GPL-3.0-only')
assert.deepEqual(pkg.dsh.bundle.platforms, ['darwin', 'linux', 'win32'])
assert.equal(pkg.dsh.client.immediately, true)
assert.equal(pkg.dependencies, undefined); assert.equal(pkg.optionalDependencies, undefined)
for (const [name, value] of Object.entries(lock.packages)) assert.notEqual(value.hasInstallScript, true, 'Install script: ' + name)
for (const file of pkg.files) {
  assert(!/\*|private|vendor|test|secret/i.test(file), 'Only explicit public runtime files belong in the package')
  await access(new URL('../' + file, import.meta.url))
  if (/\.m?js$/.test(file)) assert.equal(spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' }).status, 0, file)
}
assert.equal(typeof (await import('../skills/dsh-plugin.mjs')).apply, 'function')
const patch = await readFile(new URL('../skills/dsh.patch.yml', import.meta.url), 'utf8')
assert(patch.includes('name: ' + pkg.name))
const dictionaries = Object.fromEntries(await Promise.all(['en', 'zh'].map(async lang => [lang, JSON.parse(await readFile(new URL('../skills/' + lang + '.json', import.meta.url), 'utf8'))])))
assert.deepEqual(Object.keys(dictionaries.en).sort(), Object.keys(dictionaries.zh).sort())
const client = await readFile(new URL('../skills/dsh-client.js', import.meta.url), 'utf8')
assert(client.includes('const dictionaries = /* OCV_LOCALES */ ' + JSON.stringify(dictionaries)))
for (const match of client.matchAll(/\bt\('([^']+)'\)/g)) assert(Object.hasOwn(dictionaries.en, match[1]), 'Translation: ' + match[1])
for (const file of ['README.md', 'README_ZH.md']) assert((await readFile(new URL('../' + file, import.meta.url), 'utf8')).includes('dsh plugin --profile web add github:oneclickvirt/oneclickvirt'))
assert((await readFile(new URL('../server/constant/version.go', import.meta.url), 'utf8')).includes('ServerVersion = "' + VERSION + '"'))
assert((await readFile(new URL('../skills/SKILL.md', import.meta.url), 'utf8')).includes('version: "' + VERSION + '"'))
console.error('DSH manifest, versions, translations, syntax and dependency install scripts verified.')

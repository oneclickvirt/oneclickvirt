import assert from 'node:assert/strict'
import { readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

export const versionFiles = ['server/constant/version.go', 'package.json', 'package-lock.json', 'skills/dsh-service.mjs', 'skills/dsh-client.js', 'skills/SKILL.md', 'skills/SKILL_ZH.md']

export function packageVersion(tag) {
  const match = /^v(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})$/.exec(tag)
  assert(match, 'Use the controller release tag vYYYYMMDD-HHMMSS')
  const [, year, month, day, hour, minute, second] = match
  const date = new Date(`${year}-${month}-${day}T${hour}:${minute}:${second}Z`)
  assert(!Number.isNaN(date.valueOf()) && date.toISOString().slice(0, 19) === `${year}-${month}-${day}T${hour}:${minute}:${second}`, 'Invalid controller release timestamp')
  // npm requires SemVer. Both numeric parts preserve timestamp ordering;
  // the UI, Skill and controller continue to use the original release tag.
  return `${year}${month}${day}.${Number(hour + minute + second)}.0`
}

export async function synchronizeVersion(root, tag, check = false) {
  const version = packageVersion(tag), updates = []
  for (const file of versionFiles) {
    const location = new URL(file, root), original = await readFile(location, 'utf8')
    let updated
    if (file === 'package.json') {
      updated = original.replace(/("version"\s*:\s*")[^"]+(")/, (_match, prefix, suffix) => prefix + version + suffix)
      assert.equal(JSON.parse(updated).version, version)
    } else if (file === 'package-lock.json') {
      const lock = JSON.parse(original)
      lock.version = version; lock.packages[''].version = version
      updated = JSON.stringify(lock, null, 2) + '\n'
    } else {
      const pattern = file.endsWith('.go') ? /const ServerVersion = "[^"]+"/
        : file.endsWith('dsh-service.mjs') ? /export const VERSION = '[^']+'/
        : file.endsWith('dsh-client.js') ? /const releaseVersion = \/\* OCV_RELEASE \*\/ '[^']+'/
        : /  version: "[^"]+"/
      const value = file.endsWith('.go') ? `const ServerVersion = "${tag}"`
        : file.endsWith('dsh-service.mjs') ? `export const VERSION = '${tag}'`
        : file.endsWith('dsh-client.js') ? `const releaseVersion = /* OCV_RELEASE */ '${tag}'`
        : `  version: "${tag}"`
      assert(pattern.test(original), 'Missing version field in ' + file)
      updated = original.replace(pattern, value)
    }
    if (updated !== original) updates.push([location, updated, file])
  }
  if (check) assert.equal(updates.length, 0, 'Release version mismatch: ' + updates.map(item => item[2]).join(', '))
  else for (const [location, content] of updates) await writeFile(location, content)
  return { tag, packageVersion: version, changed: updates.map(item => item[2]) }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  const args = process.argv.slice(2), check = args.includes('--check'), tag = args.find(value => !value.startsWith('--'))
  const root = new URL('../', import.meta.url)
  const source = await readFile(new URL('server/constant/version.go', root), 'utf8')
  const selected = tag || /const ServerVersion = "([^"]+)"/.exec(source)?.[1]
  console.log(JSON.stringify(await synchronizeVersion(root, selected, check)))
}

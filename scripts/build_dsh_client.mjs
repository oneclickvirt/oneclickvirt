import { readFile, writeFile } from 'node:fs/promises'
const root = new URL('../', import.meta.url)
const dictionaries = Object.fromEntries(await Promise.all(['en', 'zh'].map(async lang => [lang, JSON.parse(await readFile(new URL('skills/' + lang + '.json', root), 'utf8'))])))
const location = new URL('skills/dsh-client.js', root), original = await readFile(location, 'utf8')
const updated = original.replace(/const dictionaries = \/\* OCV_LOCALES \*\/ .*/, 'const dictionaries = /* OCV_LOCALES */ ' + JSON.stringify(dictionaries))
if (process.argv.includes('--check')) { if (updated !== original) throw new Error('Run npm run build to update translations') }
else if (updated !== original) await writeFile(location, updated)

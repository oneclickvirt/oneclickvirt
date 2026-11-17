import { mkdir, lstat, readFile, open, rename, rm } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { randomUUID } from 'node:crypto'

export const SESSION_RECORD = 'oneclickvirt/platform-session'
export const dataDirectory = () => path.join(process.platform === 'win32' ? process.env.LOCALAPPDATA || os.homedir() : process.platform === 'darwin' ? path.join(os.homedir(), 'Library', 'Application Support') : process.env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share'), 'oneclickvirt-dsh')

// Older/headless hosts may lack credentials. The fallback remains host-only,
// with an atomic, owner-only file; browser storage never receives a token.
export class SessionStore {
  constructor(directory = dataDirectory()) { this.directory = directory; this.provider = null; this.migration = Promise.resolve() }
  get kind() { return this.provider ? 'dsh-credentials' : 'private-file' }
  attach(provider) {
    // Import a fallback login only once. A logout tombstone in either store
    // wins over an older login; upgrading DSH must never resurrect it.
    this.migration = this.migration.then(async () => {
      const fallback = await this.readFile()
      if (fallback) await provider.modifyRecord(SESSION_RECORD, current => current === undefined ? fallback : undefined)
      this.provider = provider
      if (fallback) await this.modifyFile(() => ({ kind: 'grant', payload: { signedOut: true } }))
    })
    return this.migration
  }
  async prepare() {
    await mkdir(this.directory, { recursive: true, mode: 0o700 })
    const stat = await lstat(this.directory)
    if (!stat.isDirectory() || stat.isSymbolicLink() || (process.platform !== 'win32' && ((stat.mode & 0o077) || stat.uid !== process.getuid()))) throw new Error('Credential directory must be private and owned by the DSH user')
  }
  async read() {
    await this.migration
    if (this.provider) return this.provider.readRecord(SESSION_RECORD)
    return this.readFile()
  }
  async readFile() {
    try {
      await this.prepare()
      const file = path.join(this.directory, 'session.json'), stat = await lstat(file)
      if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 32768 || (process.platform !== 'win32' && ((stat.mode & 0o077) || stat.uid !== process.getuid()))) throw new Error('Credential file must be private')
      return JSON.parse(await readFile(file, 'utf8'))
    } catch (error) { if (error.code === 'ENOENT') return undefined; throw error }
  }
  async modify(mutate) {
    await this.migration
    if (this.provider) return this.provider.modifyRecord(SESSION_RECORD, mutate)
    return this.modifyFile(mutate)
  }
  async modifyFile(mutate) {
    await this.prepare()
    const lockPath = path.join(this.directory, 'session.lock')
    let lock
    for (let attempt = 0; attempt < 100; attempt++) {
      try { lock = await open(lockPath, 'wx', 0o600); await lock.writeFile(JSON.stringify({ pid: process.pid })); break }
      catch (error) {
        if (error.code !== 'EEXIST') throw error
        try {
          const stat = await lstat(lockPath)
          if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Invalid credential lock')
          let alive = true
          try {
            const owner = JSON.parse(await readFile(lockPath, 'utf8'))
            if (Number.isSafeInteger(owner.pid) && owner.pid > 0) { try { process.kill(owner.pid, 0) } catch (e) { if (e.code === 'ESRCH') alive = false } }
          } catch { if (Date.now() - stat.mtimeMs > 60000) alive = false }
          if (!alive) { await rm(lockPath, { force: true }); continue }
        } catch (e) { if (e.code !== 'ENOENT') throw e }
        await new Promise(resolve => setTimeout(resolve, 25))
      }
    }
    if (!lock) throw new Error('Credential store is busy; try again')
    try {
      const current = await this.readFile(), next = await mutate(current)
      if (next === undefined) return current
      const temporary = path.join(this.directory, 'session-' + randomUUID() + '.tmp')
      try {
        const handle = await open(temporary, 'wx', 0o600)
        try { await handle.writeFile(JSON.stringify(next)); await handle.sync() } finally { await handle.close() }
        await rename(temporary, path.join(this.directory, 'session.json'))
      } finally { await rm(temporary, { force: true }) }
      return next
    } finally { await lock.close(); await rm(lockPath, { force: true }) }
  }
}

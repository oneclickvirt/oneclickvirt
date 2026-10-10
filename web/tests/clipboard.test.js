import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'

function clipboardFixture({ secure = false, clipboard, prompt } = {}) {
  const source = readFileSync(new URL('../src/utils/clipboard.js', import.meta.url), 'utf8')
    .replace(/^import .*$/gm, '').replace(/export (async )?function/g, '$1function').replace(/export default [\s\S]*$/, '')
  const events = [], body = { appendChild() {}, removeChild() {} }
  const context = {
    navigator: { clipboard }, window: { isSecureContext: secure }, console,
    i18n: { global: { t: key => key } },
    ElMessage: { success: x => events.push(x), warning: x => events.push(x), error: x => events.push(x) },
    ElMessageBox: { prompt: prompt || (async () => ({ value: 'echo test\nsecond line' })) },
    document: { body, activeElement: { focus: () => events.push('focus restored') },
      createElement: () => ({ style: {}, focus() {}, select() {} }),
      execCommand: () => { events.push('legacy copy'); return true } }
  }
  vm.createContext(context)
  vm.runInContext(source, context)
  return { context, events }
}

test('HTTP menu paste accepts native input and keeps multiline content', async () => {
  const { context, events } = clipboardFixture()
  assert.equal(await context.readFromClipboard(), 'echo test\nsecond line')
  assert.equal(events.length, 0)
})

test('clipboard permission denial falls back for copy and paste', async () => {
  const { context, events } = clipboardFixture({ secure: true, clipboard: {
    readText: async () => { throw new Error('denied') }, writeText: async () => { throw new Error('denied') }
  } })
  assert.equal(await context.copyToClipboard('command'), true)
  assert.ok(events.includes('legacy copy'))
  assert.ok(events.includes('focus restored'))
  assert.equal(await context.readFromClipboard(), 'echo test\nsecond line')
})

test('duplicate menu clicks do not paste a command twice and cancel sends nothing', async () => {
  let finish
  const { context } = clipboardFixture({ prompt: () => new Promise(resolve => { finish = resolve }) })
  const first = context.readFromClipboard()
  assert.equal(await context.readFromClipboard(), null)
  finish({ value: 'once' })
  assert.equal(await first, 'once')
  const cancelled = clipboardFixture({ prompt: async () => { throw new Error('cancel') } })
  assert.equal(await cancelled.context.readFromClipboard(), null)
})

test('native paste shortcuts remain available to xterm in every terminal', () => {
  for (const file of ['AdminProviderTerminal.vue', 'SSHTerminal.vue', 'ConsoleTerminal.vue', 'GlobalSSHManager.vue']) {
    const source = readFileSync(new URL('../src/components/' + file, import.meta.url), 'utf8')
    const start = source.indexOf('terminal.attachCustomKeyEventHandler')
    const handler = source.slice(start, source.indexOf('});', start) > 0 ? source.indexOf('});', start) : source.indexOf('\n  })', start))
    assert.ok(handler.includes("event.type !== 'keydown'"), file)
    assert.equal(handler.includes('pasteFromClipboard()'), false, file)
  }
})

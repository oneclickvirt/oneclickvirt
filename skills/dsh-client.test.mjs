import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile, mkdtemp, rm } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { JSDOM } from 'jsdom'
import React, { act } from 'react'
import { fixture } from './dsh-fixture.mjs'
import { apply } from './dsh-plugin.mjs'
test('native page keeps mounted state across navigation; both roles can create and edit aliases without leaking browser credentials', async t => {
  const backend = await fixture(), directory = await mkdtemp(path.join(os.tmpdir(), 'ocv-client-'))
  const effects = [], slots = new Map(); let endpoint
  apply({ effect: fn => effects.push(fn()), inject: (names, fn) => { if (names[0] === 'connection') fn({ connection: { fetch: { register: route => { endpoint = route.fetch } } } }) } }, { stateDir: directory, environment: {} })
  const dom = new JSDOM('<div id="root"></div>', { url: 'http://dsh.local', runScripts: 'outside-only', pretendToBeVisual: true }), window = dom.window
  const previous = { window: globalThis.window, document: globalThis.document, act: globalThis.IS_REACT_ACT_ENVIRONMENT }
  globalThis.window = window; globalThis.document = window.document; globalThis.IS_REACT_ACT_ENVIRONMENT = true
  window.ResizeObserver = class { observe() {} disconnect() {} }
  window.HTMLElement.prototype.getBoundingClientRect = () => ({ top: 0, left: 0, width: 1200, height: 900 })
  // The bridge constructs Node Requests; use its AbortSignal realm as well.
  window.AbortController = globalThis.AbortController; window.AbortSignal = globalThis.AbortSignal
  const pending = new Set()
  window.fetch = async (_url, options) => {
    const request = endpoint(new Request('http://dsh.local/api/oneclickvirt.plugin', options)); pending.add(request)
    try { return await request } finally { pending.delete(request) }
  }
  let plugin, dict
  window.__ModuleLoader__ = { load: module => { plugin = module.factory(() => React) } }
  window.eval(await readFile(new URL('./dsh-client.js', import.meta.url), 'utf8'))
  const uiContext = { locale: { register: (_id, values) => { dict = values.en }, bind: () => key => dict[key] || key }, effect: fn => effects.push(fn()), layout: { selectPanel() {} }, slots: { inject: (_name, fn) => { fn(); return () => {} }, register: (config, component) => { slots.set(config.name, component); return () => {} } } }
  plugin.apply(uiContext)
  const { createRoot } = await import('react-dom/client')
  const root = createRoot(window.document.getElementById('root'))
  const render = show => root.render(React.createElement(React.Fragment, null, show && React.createElement(slots.get('main')), React.createElement(slots.get('shell.overlay'), { t: key => dict[key] || key })))
  const settle = async (fn = () => {}) => { await act(async () => { await fn(); await new Promise(resolve => setTimeout(resolve, 70)) }) }
  const waitFor = async predicate => { const deadline = Date.now() + 5000; while (!predicate() && Date.now() < deadline) await settle(); assert(predicate(), window.document.querySelector('.ocv-error')?.textContent || 'page did not settle') }
  const elements = label => [...window.document.querySelectorAll('button')].filter(node => node.textContent === label)
  const click = label => { const node = elements(label)[0]; assert(node, 'button: ' + label); assert(!node.disabled, 'enabled: ' + label); node.click() }
  const change = (input, value) => {
    assert(input)
    const prototype = input.tagName === 'SELECT' ? window.HTMLSelectElement.prototype : window.HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(prototype, 'value').set.call(input, value)
    input.dispatchEvent(new window.Event(input.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }))
    input.dispatchEvent(new window.Event('change', { bubbles: true }))
  }
  const named = label => [...window.document.querySelectorAll('label')].find(node => node.firstChild?.textContent === label)?.querySelector('input,select')
  t.after(async () => { await act(() => root.unmount()); for (const effect of effects.reverse()) await effect?.(); await Promise.allSettled([...pending]); window.close(); Object.assign(globalThis, { window: previous.window, document: previous.document, IS_REACT_ACT_ENVIRONMENT: previous.act }); await backend.close(); await rm(directory, { recursive: true, force: true }) })
  await settle(() => render(true))
  for (const role of ['admin', 'user']) {
    await settle(() => { change(named('Deployment URL'), backend.baseUrl); change(named('Username'), role); change(named('Password'), 'fixture-password') })
    await settle(() => window.document.querySelector('form').dispatchEvent(new window.Event('submit', { bubbles: true, cancelable: true })))
    await waitFor(() => window.document.body.textContent.includes(role + ' · ' + role) || window.document.querySelector('.ocv-error'))
    assert(window.document.body.textContent.includes(role + ' · ' + role), window.document.querySelector('.ocv-error')?.textContent || 'login not completed')
    for (let attempt = 0; attempt < 30 && !window.document.body.textContent.includes('cpuUsage') && !window.document.querySelector('.ocv-error'); attempt++) await settle()
    assert.equal(window.document.querySelector('.ocv-error'), null, 'overview request must validate')
    assert(window.document.body.textContent.includes('cpuUsage'), 'overview loaded')
    await settle(() => click('Instances'))
    await waitFor(() => window.document.body.textContent.includes('host-original'))
    assert(window.document.body.textContent.includes('host-original'))
    await settle(() => elements('Display alias')[0].click())
    await settle(() => change(window.document.querySelector('[role="dialog"] input'), 'New alias'))
    await settle(() => click('Confirm operation'))
    await waitFor(() => backend.state.instances[0].alias === 'New alias' && !elements('Sign out')[0].disabled)
    assert.equal(backend.state.instances[0].alias, 'New alias')
    const panel = window.document.querySelector('.ocv')
    await settle(() => render(false)); assert.equal(window.document.querySelector('[data-oneclickvirt-keeper]').hidden, true)
    await settle(() => render(true)); assert.equal(window.document.querySelector('.ocv'), panel)
    assert.equal(window.document.querySelector('[role="tab"][aria-selected="true"]').textContent, 'Instances')
    await settle(() => click('Create instance'))
    await settle(() => change(named('Node'), '3'))
    for (let attempt = 0; attempt < 30 && named('Image').disabled; attempt++) await settle()
    assert([...named('Image').options].some(option => option.value === '9'), named('Image').outerHTML + ' error=' + window.document.querySelector('.ocv-error')?.textContent)
    await settle(() => change(named('Image'), '9'))
    assert.equal(named('Image').value, '9', 'selected image')
    if (role === 'user') await settle(() => { for (const [label, value] of [['CPU cores', 'cpu-1'], ['Memory (MB)', 'memory-1'], ['Disk (GB)', 'disk-1'], ['Bandwidth (Mbps)', 'bandwidth-1']]) change(named(label), value) })
    await settle(() => { const form = named('Image').closest('form'); form.dispatchEvent(new window.Event('submit', { bubbles: true, cancelable: true })) })
    await waitFor(() => window.document.body.textContent.includes('Submitted.') || window.document.querySelector('.ocv-error'))
    assert(window.document.body.textContent.includes('Submitted.'), window.document.querySelector('.ocv-error')?.textContent || JSON.stringify(backend.state.requests.at(-1)))
    assert.equal(window.document.querySelector('[role="tab"][aria-selected="true"]').textContent, 'Tasks')
    const stored = window.localStorage.getItem('oneclickvirt.dsh.ui.v1')
    assert(!/fixture-password|device-token|test-admin-token|test-user-token/.test(stored))
    await settle(() => click('Sign out')); assert(!window.document.body.textContent.includes('host-original'))
    await settle()
    assert.equal(window.document.querySelector('.ocv-error'), null, 'sign-out must not show a background polling error')
  }
})

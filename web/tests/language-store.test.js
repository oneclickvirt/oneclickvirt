import test from 'node:test'
import assert from 'node:assert/strict'
import { createPinia } from 'pinia'
import { useLanguageStore } from '../src/pinia/modules/language.js'

function mockBrowser(t, language, values = {}) {
  const storage = new Map(Object.entries(values))
  const properties = {
    navigator: { language },
    localStorage: {
      getItem: key => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, value),
      removeItem: key => storage.delete(key)
    }
  }
  for (const [key, value] of Object.entries(properties)) {
    const original = Object.getOwnPropertyDescriptor(globalThis, key)
    Object.defineProperty(globalThis, key, { value, configurable: true })
    t.after(() => {
      if (original) Object.defineProperty(globalThis, key, original)
      else delete globalThis[key]
    })
  }
}

test('an English system default replaces an unscoped legacy Chinese preference', t => {
  mockBrowser(t, 'zh-CN', { language: 'zh-CN' })
  const store = useLanguageStore(createPinia())
  store.setSystemConfigLanguage('en-US', 'saved-setting')
  assert.equal(store.initLanguage(), 'en-US')
})

test('manual choices survive reloads until the administrator changes the default again', t => {
  mockBrowser(t, 'zh-CN')
  const store = useLanguageStore(createPinia())
  store.setSystemConfigLanguage('en-US', 'initial-setting')
  assert.equal(store.initLanguage(), 'en-US')
  assert.equal(store.toggleLanguage(), 'zh-CN')

  const reopened = useLanguageStore(createPinia())
  reopened.setSystemConfigLanguage('en-US', 'initial-setting')
  assert.equal(reopened.initLanguage(), 'zh-CN')
  reopened.setSystemConfigLanguage('en-US', 'restored-setting')
  assert.equal(reopened.initLanguage(), 'en-US')
})

test('legacy aliases and browser language resolve to supported locales', t => {
  mockBrowser(t, 'en-GB')
  const store = useLanguageStore(createPinia())
  store.setSystemConfigLanguage('zh')
  assert.equal(store.initLanguage(), 'zh-CN')
  store.setSystemConfigLanguage('')
  assert.equal(store.initLanguage(), 'en-US')
})

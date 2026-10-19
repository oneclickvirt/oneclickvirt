const RELOAD_KEY = 'ocv:dynamic-import-reload'

// A deployment can replace the chunks referenced by an already open page.
// Allow one reload per built entry in this tab, including across reloads.
// Keep the marker on failures and unrelated successful navigations so a
// persistently broken deployment cannot create a reload loop.
export function recoverDynamicImport(error, { entryUrl, storage, reload }) {
  const message = String(error?.message || error || '')
  if (!/failed to fetch dynamically imported module|importing a module script failed|error loading dynamically imported module/i.test(message)) {
    return false
  }
  try {
    const session = storage()
    if (!entryUrl || session.getItem(RELOAD_KEY) === entryUrl) return false
    session.setItem(RELOAD_KEY, entryUrl)
    reload()
    return true
  } catch {
    // Without persistent tab storage, reloading cannot be safely bounded.
    return false
  }
}

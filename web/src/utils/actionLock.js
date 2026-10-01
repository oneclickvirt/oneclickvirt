/**
 * Small, framework independent guards for UI mutations.
 *
 * The lock is acquired synchronously before the first await so a rapid
 * double click cannot open two confirmation dialogs or submit two requests.
 */
export function createActionLock() {
  let locked = false

  return {
    isLocked: () => locked,
    tryAcquire: () => {
      if (locked) return false
      locked = true
      return true
    },
    release: () => {
      locked = false
    }
  }
}

export function createKeyedActionLock() {
  const lockedKeys = new Set()

  const normalizeKey = key => String(key ?? '')

  return {
    isLocked: key => lockedKeys.has(normalizeKey(key)),
    tryAcquire: key => {
      const normalized = normalizeKey(key)
      if (!normalized || lockedKeys.has(normalized)) return false
      lockedKeys.add(normalized)
      return true
    },
    release: key => {
      lockedKeys.delete(normalizeKey(key))
    },
    clear: () => lockedKeys.clear()
  }
}

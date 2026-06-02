export const DEFAULT_PROVIDER_LEVEL_LIMITS = {
  1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400, expiryDays: 0 },
  2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800, expiryDays: 0 },
  3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200, expiryDays: 0 },
  4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600, expiryDays: 0 },
  5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000, expiryDays: 0 }
}

export const DEFAULT_QUOTA_LEVEL_LIMITS = {
  1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 1024, bandwidth: 100 }, maxTraffic: 102400, expiryDays: 0 },
  2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 2048, bandwidth: 200 }, maxTraffic: 204800, expiryDays: 0 },
  3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 4096, bandwidth: 500 }, maxTraffic: 409600, expiryDays: 0 },
  4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 8192, bandwidth: 1000 }, maxTraffic: 819200, expiryDays: 0 },
  5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 16384, bandwidth: 2000 }, maxTraffic: 1638400, expiryDays: 0 }
}

export const DEFAULT_LEVEL_LIMITS = DEFAULT_PROVIDER_LEVEL_LIMITS

const LEVEL_TAG_TYPES = ['', 'success', 'info', 'warning', 'danger', 'primary']

export function cloneLevelLimit(limit = {}) {
  return {
    maxInstances: Number(limit.maxInstances ?? limit['max-instances'] ?? 1),
    maxTraffic: Number(limit.maxTraffic ?? limit['max-traffic'] ?? 102400),
    expiryDays: Number(limit.expiryDays ?? limit['expiry-days'] ?? 0),
    maxResources: {
      cpu: Number(limit.maxResources?.cpu ?? limit['max-resources']?.cpu ?? 1),
      memory: Number(limit.maxResources?.memory ?? limit['max-resources']?.memory ?? 512),
      disk: Number(limit.maxResources?.disk ?? limit['max-resources']?.disk ?? 10240),
      bandwidth: Number(limit.maxResources?.bandwidth ?? limit['max-resources']?.bandwidth ?? 100)
    }
  }
}

export function buildDefaultLevelLimit(level, previousLimit = null, defaults = DEFAULT_LEVEL_LIMITS) {
  if (defaults[level]) {
    return cloneLevelLimit(defaults[level])
  }
  const fallbackLevel = getSortedLevelKeys(defaults).at(-1)
  const base = previousLimit ? cloneLevelLimit(previousLimit) : cloneLevelLimit(defaults[fallbackLevel] || DEFAULT_LEVEL_LIMITS[5])
  return {
    maxInstances: Math.max(1, base.maxInstances),
    maxTraffic: Math.max(1024, base.maxTraffic),
    expiryDays: base.expiryDays || 0,
    maxResources: {
      cpu: Math.max(1, base.maxResources.cpu),
      memory: Math.max(128, base.maxResources.memory),
      disk: Math.max(512, base.maxResources.disk),
      bandwidth: Math.max(1, base.maxResources.bandwidth)
    }
  }
}

export function getSortedLevelKeys(levelLimits = {}) {
  return Object.keys(levelLimits)
    .map(level => Number(level))
    .filter(level => Number.isInteger(level) && level > 0)
    .sort((a, b) => a - b)
}

export function normalizeLevelLimits(rawLevelLimits = {}, defaults = DEFAULT_LEVEL_LIMITS) {
  const result = {}
  const source = Object.keys(rawLevelLimits).length > 0 ? rawLevelLimits : defaults
  for (const level of getSortedLevelKeys(source)) {
    result[level] = cloneLevelLimit(source[level])
  }
  if (Object.keys(result).length === 0) {
    result[1] = buildDefaultLevelLimit(1, null, defaults)
  }
  return result
}

export function formatLevelLimitsForBackend(levelLimits = {}) {
  const result = {}
  for (const level of getSortedLevelKeys(levelLimits)) {
    const limit = cloneLevelLimit(levelLimits[level])
    result[level] = {
      'max-instances': limit.maxInstances,
      'max-traffic': limit.maxTraffic,
      'expiry-days': limit.expiryDays,
      'max-resources': {
        cpu: limit.maxResources.cpu,
        memory: limit.maxResources.memory,
        disk: limit.maxResources.disk,
        bandwidth: limit.maxResources.bandwidth
      }
    }
  }
  return result
}

export function getLevelTagType(level) {
  return LEVEL_TAG_TYPES[(Number(level) - 1) % LEVEL_TAG_TYPES.length] || ''
}

export const expectedApiContract = '2026-08-16.1'

export function isUnmatchedApiRoute(error) {
  if (Number(error?.response?.status) !== 404) return false

  const data = error?.response?.data
  if (typeof data === 'string') {
    return /404 page not found|\bnot found\b/i.test(data.trim())
  }

  const message = `${data?.message || ''} ${data?.msg || ''}`.trim()
  return /API endpoint not found/i.test(message)
}

export function isApiContractMismatch(buildInfo) {
  const data = buildInfo?.data && typeof buildInfo.data === 'object' ? buildInfo.data : buildInfo
  if (!data || typeof data !== 'object') return false
  return data.apiContract !== expectedApiContract
}

export function normalizeShareURL(url) {
  if (!url) return ''
  if (/^https?:\/\//i.test(url)) return url
  const prefix = url.startsWith('/') ? '' : '/'
  return `${window.location.origin}${prefix}${url}`
}

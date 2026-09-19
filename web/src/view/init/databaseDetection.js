export function databaseRequestMatches(form, request) {
  return Object.entries(request).every(([key, value]) => form[key] === value)
}

export function applyDetectedDatabaseType(form, detected) {
  const type = String(detected?.type ?? '').trim().toLowerCase()
  if (type !== 'mysql' && type !== 'mariadb') return false
  form.type = type
  return true
}

export function fillDatabasePort(form, type) {
  if ((type === 'mysql' || type === 'mariadb') && !String(form.port ?? '').trim()) {
    form.port = '3306'
  }
}

// Existing API/imported names must not prevent editing unrelated settings.
// New names and renames retain the current short-name contract.
export function providerNameValidationError(value, { isEditing = false, originalName = '' } = {}) {
  if (!value) return 'serverNameRequired'
  if (isEditing && value === originalName) return ''
  if (!/^[a-zA-Z0-9]+$/.test(value)) return 'serverNamePattern'
  if (value.length > 7) return 'serverNameMaxLength'
  return ''
}

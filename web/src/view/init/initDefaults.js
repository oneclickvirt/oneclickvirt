export const INIT_STEPS = Object.freeze(['database', 'admin', 'user'])

const isBlank = (value) => value == null || (typeof value === 'string' && value.trim() === '')

const fillBlank = (target, key, value) => {
  if (!isBlank(target[key])) return false
  target[key] = value
  return true
}

const fillAccountDefaults = (account, defaults, generatePassword) => {
  let changed = false
  changed = fillBlank(account, 'username', defaults.username) || changed
  changed = fillBlank(account, 'email', defaults.email) || changed

  const passwordBlank = isBlank(account.password)
  const confirmationBlank = isBlank(account.confirmPassword)
  if (passwordBlank || confirmationBlank) {
    const password = !passwordBlank
      ? account.password
      : !confirmationBlank
        ? account.confirmPassword
        : generatePassword()
    if (passwordBlank) {
      account.password = password
      changed = true
    }
    if (confirmationBlank) {
      account.confirmPassword = password
      changed = true
    }
  }

  return changed
}

export const fillInitDefaults = ({
  activeStep,
  databaseForm,
  initForm,
  recommendedDatabaseType = 'mysql',
  generatePassword
}) => {
  const activeIndex = INIT_STEPS.indexOf(activeStep)
  if (activeIndex < 0) {
    throw new Error(`Unknown initialization step: ${activeStep}`)
  }
  if (typeof generatePassword !== 'function') {
    throw new TypeError('generatePassword must be a function')
  }

  let changed = false
  if (activeIndex <= INIT_STEPS.indexOf('database')) {
    const databaseType = isBlank(recommendedDatabaseType) ? 'mysql' : recommendedDatabaseType
    changed = fillBlank(databaseForm, 'type', databaseType) || changed
    changed = fillBlank(databaseForm, 'host', '127.0.0.1') || changed
    changed = fillBlank(databaseForm, 'port', '3306') || changed
    changed = fillBlank(databaseForm, 'database', 'oneclickvirt') || changed
    changed = fillBlank(databaseForm, 'username', 'root') || changed
  }

  if (activeIndex <= INIT_STEPS.indexOf('admin')) {
    changed = fillAccountDefaults(initForm.admin, {
      username: 'admin',
      email: 'admin@spiritlhl.net'
    }, generatePassword) || changed
  }

  if (activeIndex <= INIT_STEPS.indexOf('user')) {
    changed = fillAccountDefaults(initForm.user, {
      username: 'testuser',
      email: 'user@spiritlhl.net'
    }, generatePassword) || changed
  }

  return changed
}

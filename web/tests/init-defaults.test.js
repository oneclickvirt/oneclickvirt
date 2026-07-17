import test from 'node:test'
import assert from 'node:assert/strict'
import { fillInitDefaults } from '../src/view/init/initDefaults.js'

const createForms = () => ({
  databaseForm: {
    type: 'mysql',
    host: '127.0.0.1',
    port: '3306',
    database: 'oneclickvirt',
    username: 'root',
    password: ''
  },
  initForm: {
    admin: {
      username: '',
      password: '',
      confirmPassword: '',
      email: ''
    },
    user: {
      username: '',
      password: '',
      confirmPassword: '',
      email: '',
      enabled: false
    }
  }
})

test('user-step fill leaves every previous-step value untouched', () => {
  const forms = createForms()
  Object.assign(forms.databaseForm, { host: '', password: 'DatabaseSecret' })
  Object.assign(forms.initForm.admin, {
    username: 'custom-admin',
    password: 'AdminSecret1!',
    confirmPassword: 'AdminSecret1!',
    email: 'owner@example.com'
  })
  forms.initForm.user.enabled = true

  const changed = fillInitDefaults({
    activeStep: 'user',
    ...forms,
    generatePassword: () => 'UserGenerated1!'
  })

  assert.equal(changed, true)
  assert.equal(forms.databaseForm.host, '')
  assert.equal(forms.databaseForm.password, 'DatabaseSecret')
  assert.deepEqual(forms.initForm.admin, {
    username: 'custom-admin',
    password: 'AdminSecret1!',
    confirmPassword: 'AdminSecret1!',
    email: 'owner@example.com'
  })
  assert.deepEqual(forms.initForm.user, {
    username: 'testuser',
    password: 'UserGenerated1!',
    confirmPassword: 'UserGenerated1!',
    email: 'user@spiritlhl.net',
    enabled: true
  })
})

test('admin-step fill only completes blank admin and future user fields', () => {
  const forms = createForms()
  forms.databaseForm.host = ''
  Object.assign(forms.initForm.admin, {
    username: 'chosen-admin',
    password: 'ChosenAdmin1!',
    confirmPassword: '',
    email: ''
  })
  forms.initForm.user.username = 'chosen-user'
  const generated = ['FutureUser1!']

  fillInitDefaults({
    activeStep: 'admin',
    ...forms,
    generatePassword: () => generated.shift()
  })

  assert.equal(forms.databaseForm.host, '')
  assert.deepEqual(forms.initForm.admin, {
    username: 'chosen-admin',
    password: 'ChosenAdmin1!',
    confirmPassword: 'ChosenAdmin1!',
    email: 'admin@spiritlhl.net'
  })
  assert.deepEqual(forms.initForm.user, {
    username: 'chosen-user',
    password: 'FutureUser1!',
    confirmPassword: 'FutureUser1!',
    email: 'user@spiritlhl.net',
    enabled: false
  })
})

test('database-step fill completes known blanks through all later steps', () => {
  const forms = createForms()
  Object.assign(forms.databaseForm, {
    type: '',
    host: '   ',
    port: '',
    database: '',
    username: '',
    password: 'KeepDatabasePassword'
  })
  const generated = ['AdminGenerated1!', 'UserGenerated1!']

  fillInitDefaults({
    activeStep: 'database',
    ...forms,
    recommendedDatabaseType: 'mariadb',
    generatePassword: () => generated.shift()
  })

  assert.deepEqual(forms.databaseForm, {
    type: 'mariadb',
    host: '127.0.0.1',
    port: '3306',
    database: 'oneclickvirt',
    username: 'root',
    password: 'KeepDatabasePassword'
  })
  assert.equal(forms.initForm.admin.password, 'AdminGenerated1!')
  assert.equal(forms.initForm.user.password, 'UserGenerated1!')
})

test('an existing confirmation fills only its blank password partner', () => {
  const forms = createForms()
  Object.assign(forms.initForm.user, {
    username: 'existing-user',
    password: '',
    confirmPassword: 'ExistingConfirmation1!',
    email: 'user@example.com'
  })

  fillInitDefaults({
    activeStep: 'user',
    ...forms,
    generatePassword: () => assert.fail('password generator should not run')
  })

  assert.equal(forms.initForm.user.password, 'ExistingConfirmation1!')
  assert.equal(forms.initForm.user.confirmPassword, 'ExistingConfirmation1!')
})

test('filled values, including mismatched passwords and enabled state, are never overwritten', () => {
  const forms = createForms()
  Object.assign(forms.initForm.user, {
    username: 'existing-user',
    password: 'FirstPassword1!',
    confirmPassword: 'DifferentPassword1!',
    email: 'user@example.com',
    enabled: true
  })

  const changed = fillInitDefaults({
    activeStep: 'user',
    ...forms,
    generatePassword: () => assert.fail('password generator should not run')
  })

  assert.equal(changed, false)
  assert.equal(forms.initForm.user.password, 'FirstPassword1!')
  assert.equal(forms.initForm.user.confirmPassword, 'DifferentPassword1!')
  assert.equal(forms.initForm.user.enabled, true)
})

test('unknown steps fail explicitly without mutating any form', () => {
  const forms = createForms()
  const before = JSON.parse(JSON.stringify(forms))

  assert.throws(() => fillInitDefaults({
    activeStep: 'unknown',
    ...forms,
    generatePassword: () => 'UnusedPassword1!'
  }), /Unknown initialization step/)
  assert.deepEqual(forms, before)
})

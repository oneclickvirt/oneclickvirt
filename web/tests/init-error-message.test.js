import test from 'node:test'
import assert from 'node:assert/strict'
import { getInitErrorMessage } from '../src/view/init/initError.js'

test('extracts the first Element Plus form validation message', () => {
  const error = {
    admin: [{ message: '密码必须包含特殊字符' }],
    email: [{ message: '请输入正确的邮箱格式' }]
  }

  assert.equal(getInitErrorMessage(error, 'fallback'), '密码必须包含特殊字符')
})

test('prefers API details over generic response messages', () => {
  const error = {
    response: {
      data: {
        msg: '数据验证失败',
        details: '数据库密码错误'
      }
    }
  }

  assert.equal(getInitErrorMessage(error, 'fallback'), '数据库密码错误')
})

test('supports a direct response object in the defensive non-200 branch', () => {
  assert.equal(getInitErrorMessage({ code: 400, msg: '管理员密码不符合要求' }, 'fallback'), '管理员密码不符合要求')
})

test('uses normalized API userMessage before the generic Error message', () => {
  const error = {
    userMessage: '管理员密码强度不足',
    message: '[400] 数据验证失败: 管理员密码强度不足'
  }

  assert.equal(getInitErrorMessage(error, 'fallback'), '管理员密码强度不足')
})

test('falls back when an error has no displayable reason', () => {
  assert.equal(getInitErrorMessage({ message: '[object Object]' }, '初始化失败，请重试'), '初始化失败，请重试')
})

import request from '@/utils/request'

export function getCaptcha() {
  return request({
    url: '/v1/auth/captcha',
    method: 'get'
  })
}

export function register(data) {
  return request({
    url: '/v1/auth/register',
    method: 'post',
    data
  })
}

// 统一登录接口 - 后端自动识别用户类型
export function unifiedLogin(data) {
  return request({
    url: '/v1/auth/login',
    method: 'post',
    data
  })
}

export function login(data) {
  return unifiedLogin({
    ...data,
    userType: 'user'
  })
}

export function adminLogin(data) {
  return unifiedLogin({
    ...data,
    userType: 'admin'
  })
}

export function forgotPassword(data) {
  return request({
    url: '/v1/auth/forgot-password',
    method: 'post',
    data
  })
}

export function resetPassword(data) {
  return request({
    url: '/v1/auth/reset-password',
    method: 'post',
    data
  })
}

export function getUserInfo() {
  return request({
    url: '/v1/user/info',
    method: 'get'
  })
}

export function logout() {
  return request({
    url: '/v1/auth/logout',
    method: 'post'
  })
}
// 发送验证码 - 用于邮箱/TG/QQ登录
export function sendVerifyCode(data) {
  return request({
    url: '/v1/auth/send-verify-code',
    method: 'post',
    data
  })
}

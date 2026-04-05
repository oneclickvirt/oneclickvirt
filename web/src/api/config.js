import request from '@/utils/request'

// 新的统一配置API
export function getUnifiedConfig(scope = 'user') {
  return request({
    url: '/v1/config',
    method: 'get',
    params: { scope }
  })
}

export function updateUnifiedConfig(data) {
  return request({
    url: '/v1/config',
    method: 'put',
    data
  })
}

export function getConfigSnapshots() {
  return request({
    url: '/v1/config/snapshots',
    method: 'get'
  })
}

export function rollbackConfig(version, reason) {
  return request({
    url: '/v1/config/rollback',
    method: 'post',
    data: { version, reason }
  })
}

export function getRegisterConfig() {
  return request({
    url: '/v1/public/register-config',
    method: 'get'
  })
}

export function getConfig() {
  return getUnifiedConfig('user')
}

export function updateRegistrationEnabled(enabled) {
  return updateUnifiedConfig({
    scope: 'user',
    config: {
      'auth.enablePublicRegistration': enabled
    }
  })
}

export function getAdminConfig() {
  return getUnifiedConfig('admin')
}

export function updateAdminConfig(data) {
  return updateUnifiedConfig({
    scope: 'admin',
    config: data
  })
}

// 公开配置API
export function getPublicConfig() {
  return getUnifiedConfig('public')
}
// OAuth2配置API - 使用 oauth2 providers CRUD 接口
export function getOAuth2Config() {
  return request({
    url: '/v1/oauth2/providers',
    method: 'get'
  })
}

export function updateOAuth2Config(id, data) {
  if (id) {
    return request({
      url: `/v1/oauth2/providers/${id}`,
      method: 'put',
      data
    })
  }
  return request({
    url: '/v1/oauth2/providers',
    method: 'post',
    data
  })
}

export function resetOAuth2RegistrationCount(id) {
  return request({
    url: `/v1/oauth2/providers/${id}/reset-count`,
    method: 'post'
  })
}

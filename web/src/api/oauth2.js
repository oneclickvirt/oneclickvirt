import request from '@/utils/request'

// ==================== 管理员接口 ====================

/**
 * 获取所有OAuth2提供商（管理员）
 */
export function getAllOAuth2Providers() {
  return request({
    url: '/v1/oauth2/providers',
    method: 'get'
  })
}

/**
 * 获取单个OAuth2提供商
 * @param {number} id - 提供商ID
 */
export function getOAuth2Provider(id) {
  return request({
    url: `/v1/oauth2/providers/${id}`,
    method: 'get'
  })
}

/**
 * 创建OAuth2提供商
 * @param {Object} data - 提供商配置
 */
export function createOAuth2Provider(data) {
  return request({
    url: '/v1/oauth2/providers',
    method: 'post',
    data
  })
}

/**
 * 更新OAuth2提供商
 * @param {number} id - 提供商ID
 * @param {Object} data - 更新内容
 */
export function updateOAuth2Provider(id, data) {
  return request({
    url: `/v1/oauth2/providers/${id}`,
    method: 'put',
    data
  })
}

/**
 * 删除OAuth2提供商
 * @param {number} id - 提供商ID
 */
export function deleteOAuth2Provider(id) {
  return request({
    url: `/v1/oauth2/providers/${id}`,
    method: 'delete'
  })
}

/**
 * 重置OAuth2注册计数
 * @param {number} id - 提供商ID
 */
export function resetOAuth2RegistrationCount(id) {
  return request({
    url: `/v1/oauth2/providers/${id}/reset-count`,
    method: 'post'
  })
}

/**
 * 获取OAuth2预设配置列表
 */
export function getOAuth2Presets() {
  return request({
    url: '/v1/oauth2/presets',
    method: 'get'
  })
}

/**
 * 获取指定的OAuth2预设配置
 * @param {string} name - 预设名称 (linuxdo, idcflare, github, generic)
 */
export function getOAuth2Preset(name) {
  return request({
    url: `/v1/oauth2/presets/${name}`,
    method: 'get'
  })
}

// ==================== 公开接口 ====================

/**
 * 获取启用的OAuth2提供商列表（公开）
 */
export function getEnabledOAuth2Providers() {
  return request({
    url: '/v1/public/oauth2/providers',
    method: 'get'
  })
}

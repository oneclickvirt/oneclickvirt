import request from '@/utils/request'

// ============ 用户域名绑定 ============

export function getUserDomains() {
  return request({
    url: '/v1/user/domains',
    method: 'get'
  })
}

export function createUserDomain(data) {
  return request({
    url: '/v1/user/domains',
    method: 'post',
    data
  })
}

export function updateUserDomain(id, data) {
  return request({
    url: `/v1/user/domains/${id}`,
    method: 'put',
    data
  })
}

export function deleteUserDomain(id) {
  return request({
    url: `/v1/user/domains/${id}`,
    method: 'delete'
  })
}

// ============ 用户KYC ============

export function getUserKYC() {
  return request({
    url: '/v1/user/kyc',
    method: 'get'
  })
}

export function submitUserKYC(data) {
  return request({
    url: '/v1/user/kyc',
    method: 'post',
    data
  })
}

export function submitAlipayKYC(data) {
  return request({
    url: '/v1/user/kyc/alipay',
    method: 'post',
    data
  })
}

export function queryAlipayKYCResult() {
  return request({
    url: '/v1/user/kyc/alipay/result',
    method: 'get'
  })
}

// ============ 用户签到 ============

export function generateCheckinCode(instanceId) {
  return request({
    url: `/v1/user/checkin/code/${instanceId}`,
    method: 'post'
  })
}

export function doCheckin(data) {
  return request({
    url: '/v1/user/checkin',
    method: 'post',
    data
  })
}

export function getCheckinRecords(params) {
  return request({
    url: '/v1/user/checkin/records',
    method: 'get',
    params
  })
}

// ============ 管理员域名管理 ============

export function adminGetDomains() {
  return request({
    url: '/v1/admin/domains',
    method: 'get'
  })
}

export function adminDeleteDomain(id) {
  return request({
    url: `/v1/admin/domains/${id}`,
    method: 'delete'
  })
}

export function getDomainConfig(providerId) {
  return request({
    url: `/v1/admin/providers/${providerId}/domain-config`,
    method: 'get'
  })
}

export function updateDomainConfig(providerId, data) {
  return request({
    url: `/v1/admin/providers/${providerId}/domain-config`,
    method: 'put',
    data
  })
}

// ============ 管理员KYC管理 ============

export function adminGetKYCList(params) {
  return request({
    url: '/v1/admin/kyc',
    method: 'get',
    params
  })
}

export function adminReviewKYC(id, data) {
  return request({
    url: `/v1/admin/kyc/${id}/review`,
    method: 'put',
    data
  })
}

// ============ 管理员签到配置 ============

export function getCheckinConfig(providerId) {
  return request({
    url: `/v1/admin/providers/${providerId}/checkin-config`,
    method: 'get'
  })
}

export function updateCheckinConfig(providerId, data) {
  return request({
    url: `/v1/admin/providers/${providerId}/checkin-config`,
    method: 'put',
    data
  })
}

// ============ 管理员特殊操作 ============

export function adminLoginAsUser(userId) {
  return request({
    url: `/v1/admin/users/${userId}/login-as`,
    method: 'post'
  })
}

export function adminTransferInstance(data) {
  return request({
    url: '/v1/admin/instances/transfer',
    method: 'post',
    data
  })
}

// ============ 公共接口 ============

export function getBuildInfo() {
  return request({
    url: '/v1/public/build-info',
    method: 'get'
  })
}

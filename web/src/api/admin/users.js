import request from '@/utils/request'

export const getUserList = (params) => {
  return request({
    url: '/v1/admin/users',
    method: 'get',
    params
  })
}

export const createUser = (data) => {
  return request({
    url: '/v1/admin/users',
    method: 'post',
    data
  })
}

export const updateUser = (id, data) => {
  return request({
    url: `/v1/admin/users/${id}`,
    method: 'put',
    data
  })
}

export const deleteUser = (id) => {
  return request({
    url: `/v1/admin/users/${id}`,
    method: 'delete'
  })
}

export const resetUserPassword = (id) => {
  return request({
    url: `/v1/admin/users/${id}/reset-password`,
    method: 'put',
    data: {} // 空数据，后端会自动生成密码
  })
}

export const updateUserStatus = (id, status) => {
  return request({
    url: `/v1/admin/users/${id}/status`,
    method: 'put',
    data: { status }
  })
}

// 用户状态相关接口
export const toggleUserStatus = (id, status) => {
  return updateUserStatus(id, status)
}

// 批量操作相关接口
export const batchDeleteUsers = (userIds) => {
  return request({
    url: '/v1/admin/users/batch-delete',
    method: 'post',
    data: { userIds }
  })
}

export const batchUpdateUserStatus = (userIds, status) => {
  return request({
    url: '/v1/admin/users/batch-status',
    method: 'put',
    data: { userIds, status }
  })
}

export const batchUpdateUserLevel = (userIds, level) => {
  return request({
    url: '/v1/admin/users/batch-level',
    method: 'put',
    data: { userIds, level }
  })
}

export const updateUserLevel = (id, level) => {
  return request({
    url: `/v1/admin/users/${id}/level`,
    method: 'put',
    data: { level }
  })
}

export const setUserExpiry = (data) => {
  return request({
    url: '/v1/admin/users/set-expiry',
    method: 'post',
    data
  })
}

// API Token管理（管理员）
export const adminGetApiTokenList = (params) => {
  return request({
    url: '/v1/admin/api-tokens',
    method: 'get',
    params
  })
}

export const adminDeleteApiToken = (id) => {
  return request({
    url: `/v1/admin/api-tokens/${id}`,
    method: 'delete'
  })
}

export const adminBatchDeleteApiTokens = (ids) => {
  return request({
    url: '/v1/admin/api-tokens/batch-delete',
    method: 'post',
    data: { ids }
  })
}

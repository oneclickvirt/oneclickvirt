import request from '@/utils/request'

export const getAnnouncements = (params) => {
  return request({
    url: '/v1/admin/announcements',
    method: 'get',
    params
  })
}

export const createAnnouncement = (data) => {
  return request({
    url: '/v1/admin/announcements',
    method: 'post',
    data
  })
}

export const updateAnnouncement = (id, data) => {
  return request({
    url: `/v1/admin/announcements/${id}`,
    method: 'put',
    data
  })
}

export const deleteAnnouncement = (id) => {
  return request({
    url: `/v1/admin/announcements/${id}`,
    method: 'delete'
  })
}

export const batchDeleteAnnouncements = (ids) => {
  return request({
    url: '/v1/admin/announcements/batch-delete',
    method: 'post',
    data: { ids }
  })
}

export const batchUpdateAnnouncementStatus = (ids, status) => {
  return request({
    url: '/v1/admin/announcements/batch-status',
    method: 'put',
    data: { ids, status }
  })
}

export const getInviteCodes = (params) => {
  return request({
    url: '/v1/admin/invite-codes',
    method: 'get',
    params
  })
}

export const createInviteCode = (data) => {
  return request({
    url: '/v1/admin/invite-codes',
    method: 'post',
    data
  })
}

export const generateInviteCodes = (data) => {
  return request({
    url: '/v1/admin/invite-codes/generate',
    method: 'post',
    data
  })
}

export const deleteInviteCode = (id) => {
  return request({
    url: `/v1/admin/invite-codes/${id}`,
    method: 'delete'
  })
}

export const batchDeleteInviteCodes = (data) => {
  return request({
    url: '/v1/admin/invite-codes/batch-delete',
    method: 'post',
    data
  })
}

export const exportInviteCodes = (data) => {
  return request({
    url: '/v1/admin/invite-codes/export',
    method: 'get',
    params: data
  })
}

export const getRedemptionCodes = (params) => {
  return request({
    url: '/v1/admin/redemption-codes',
    method: 'get',
    params
  })
}

export const batchCreateRedemptionCodes = (data) => {
  return request({
    url: '/v1/admin/redemption-codes/batch-create',
    method: 'post',
    data
  })
}

export const exportRedemptionCodes = (data) => {
  return request({
    url: '/v1/admin/redemption-codes/export',
    method: 'post',
    data
  })
}

export const batchDeleteRedemptionCodes = (data) => {
  return request({
    url: '/v1/admin/redemption-codes/batch-delete',
    method: 'post',
    data
  })
}

// 系统镜像管理API
export const systemImageApi = {
  // 获取系统镜像列表
  getList: (params) => {
    return request({
      url: '/v1/admin/system-images',
      method: 'get',
      params
    })
  },

  // 创建系统镜像
  create: (data) => {
    return request({
      url: '/v1/admin/system-images',
      method: 'post',
      data
    })
  },

  // 更新系统镜像
  update: (id, data) => {
    return request({
      url: `/v1/admin/system-images/${id}`,
      method: 'put',
      data
    })
  },

  // 删除系统镜像
  delete: (id) => {
    return request({
      url: `/v1/admin/system-images/${id}`,
      method: 'delete'
    })
  },

  // 批量删除系统镜像
  batchDelete: (data) => {
    return request({
      url: '/v1/admin/system-images/batch-delete',
      method: 'post',
      data
    })
  },

  // 批量更新状态
  batchUpdateStatus: (data) => {
    return request({
      url: '/v1/admin/system-images/batch-status',
      method: 'put',
      data
    })
  }
}

// 封禁规则
export const blockRulesApi = {
  getRules: () => request({ url: '/v1/admin/block-rules', method: 'get' }),
  getRule: (id) => request({ url: `/v1/admin/block-rules/${id}`, method: 'get' }),
  createRule: (data) => request({ url: '/v1/admin/block-rules', method: 'post', data }),
  updateRule: (id, data) => request({ url: `/v1/admin/block-rules/${id}`, method: 'put', data }),
  deleteRule: (id) => request({ url: `/v1/admin/block-rules/${id}`, method: 'delete' }),
  applyRules: (data) => request({ url: '/v1/admin/block-rules/apply', method: 'post', data }),
  removeApplications: (data) => request({ url: '/v1/admin/block-rules/remove', method: 'post', data }),
  getApplications: (params) => request({ url: '/v1/admin/block-rules/applications', method: 'get', params }),
  getProviderBlockStatus: (id) => request({ url: `/v1/admin/providers/${id}/block-status`, method: 'get' }),
  getAgentProviders: () => request({ url: '/v1/admin/block-rules/agent-providers', method: 'get' })
}

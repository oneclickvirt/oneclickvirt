import { get, upload, download, del, post } from '@/utils/request'

export const listUserInstanceSFTP = (instanceId, remotePath = '/') => {
  return get(`/v1/user/instances/${instanceId}/sftp/list`, { path: remotePath })
}

export const downloadUserInstanceSFTP = (instanceId, remotePath) => {
  return download(`/v1/user/instances/${instanceId}/sftp/download`, { path: remotePath })
}

export const uploadUserInstanceSFTP = (instanceId, formData, config = {}) => {
  return upload(`/v1/user/instances/${instanceId}/sftp/upload`, formData, {
    timeout: 0,
    ...config
  })
}

export const getUserInstanceSFTPUploadStatus = (instanceId, params) => {
  return get(`/v1/user/instances/${instanceId}/sftp/upload/status`, params)
}

export const abortUserInstanceSFTPUpload = (instanceId, formData) => {
  return upload(`/v1/user/instances/${instanceId}/sftp/upload/abort`, formData, {
    timeout: 0
  })
}

export const listSharedInstanceSFTP = (token, remotePath = '/') => {
  return get(`/v1/public/instance-shares/${encodeURIComponent(token)}/sftp/list`, { path: remotePath })
}

export const downloadSharedInstanceSFTP = (token, remotePath) => {
  return download(`/v1/public/instance-shares/${encodeURIComponent(token)}/sftp/download`, { path: remotePath })
}

export const uploadSharedInstanceSFTP = (token, formData, config = {}) => {
  return upload(`/v1/public/instance-shares/${encodeURIComponent(token)}/sftp/upload`, formData, {
    timeout: 0,
    ...config
  })
}

export const getSharedInstanceSFTPUploadStatus = (token, params) => {
  return get(`/v1/public/instance-shares/${encodeURIComponent(token)}/sftp/upload/status`, params)
}

export const abortSharedInstanceSFTPUpload = (token, formData) => {
  return upload(`/v1/public/instance-shares/${encodeURIComponent(token)}/sftp/upload/abort`, formData, {
    timeout: 0
  })
}

export const listAdminInstanceSFTP = (instanceId, remotePath = '/') => {
  return get(`/v1/admin/instances/${instanceId}/sftp/list`, { path: remotePath })
}

export const downloadAdminInstanceSFTP = (instanceId, remotePath) => {
  return download(`/v1/admin/instances/${instanceId}/sftp/download`, { path: remotePath })
}

export const uploadAdminInstanceSFTP = (instanceId, formData, config = {}) => {
  return upload(`/v1/admin/instances/${instanceId}/sftp/upload`, formData, {
    timeout: 0,
    ...config
  })
}

export const getAdminInstanceSFTPUploadStatus = (instanceId, params) => {
  return get(`/v1/admin/instances/${instanceId}/sftp/upload/status`, params)
}

export const abortAdminInstanceSFTPUpload = (instanceId, formData) => {
  return upload(`/v1/admin/instances/${instanceId}/sftp/upload/abort`, formData, {
    timeout: 0
  })
}

export const listAdminProviderSFTP = (providerId, remotePath = '/') => {
  return get(`/v1/admin/providers/${providerId}/sftp/list`, { path: remotePath })
}

export const downloadAdminProviderSFTP = (providerId, remotePath) => {
  return download(`/v1/admin/providers/${providerId}/sftp/download`, { path: remotePath })
}

export const uploadAdminProviderSFTP = (providerId, formData, config = {}) => {
  return upload(`/v1/admin/providers/${providerId}/sftp/upload`, formData, {
    timeout: 0,
    ...config
  })
}

export const getAdminProviderSFTPUploadStatus = (providerId, params) => {
  return get(`/v1/admin/providers/${providerId}/sftp/upload/status`, params)
}

export const abortAdminProviderSFTPUpload = (providerId, formData) => {
  return upload(`/v1/admin/providers/${providerId}/sftp/upload/abort`, formData, {
    timeout: 0
  })
}

// ── Agent FM（无 SSH 凭据的 Agent 模式节点）──────────────────────────────────

export const listAdminProviderFM = (providerId, remotePath = '/') => {
  return get(`/v1/admin/providers/${providerId}/fm/list`, { path: remotePath })
}

export const downloadAdminProviderFM = (providerId, remotePath) => {
  return download(`/v1/admin/providers/${providerId}/fm/download`, { path: remotePath })
}

export const uploadAdminProviderFM = (providerId, formData, config = {}) => {
  return upload(`/v1/admin/providers/${providerId}/fm/upload`, formData, { timeout: 0, ...config })
}

export const deleteAdminProviderFM = (providerId, remotePath) => {
  return del(`/v1/admin/providers/${providerId}/fm/file`, { params: { path: remotePath } })
}

export const mkdirAdminProviderFM = (providerId, dirPath) => {
  return post(`/v1/admin/providers/${providerId}/fm/mkdir`, { path: dirPath })
}

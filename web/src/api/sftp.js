import { get, upload, download } from '@/utils/request'

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

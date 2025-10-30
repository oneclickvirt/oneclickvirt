import request from '@/utils/request'

// 获取公共公告
export const getPublicAnnouncements = (type = '') => {
  return request({
    url: '/v1/public/announcements',
    method: 'get',
    params: type ? { type } : {}
  })
}

// 获取公告列表（带分页）
export const getAnnouncements = (params) => {
  return request({
    url: '/v1/public/announcements',
    method: 'get',
    params
  })
}

// 获取公共统计数据
export const getPublicStats = () => {
  return request({
    url: '/v1/public/stats',
    method: 'get'
  })
}

// 获取系统配置（公开部分）
export const getPublicConfig = () => {
  return request({
    url: '/v1/public/register-config',
    method: 'get'
  })
}

// 获取公开的系统配置（如默认语言等）
export const getPublicSystemConfig = () => {
  return request({
    url: '/v1/public/system-config',
    method: 'get'
  })
}

// 获取可用的系统镜像列表
export const getAvailableSystemImages = (params) => {
  return request({
    url: '/v1/public/system-images/available',
    method: 'get',
    params
  })
}

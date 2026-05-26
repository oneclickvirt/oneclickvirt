import request from '@/utils/request'

export const getSystemConfig = () => {
  return request({
    url: '/v1/admin/config',
    method: 'get'
  })
}

export const updateSystemConfig = (data) => {
  return request({
    url: '/v1/admin/config',
    method: 'put',
    data
  })
}

// 日志查看
export const getLogDates = () => {
  return request({
    url: '/v1/admin/logs/dates',
    method: 'get'
  })
}

export const getLogContent = (params) => {
  return request({
    url: '/v1/admin/logs/content',
    method: 'get',
    params
  })
}

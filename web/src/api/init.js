import request from '@/utils/request'

// 检查系统是否已初始化
export const checkSystemInit = () => {
  return request({
    url: '/v1/public/init/check',
    method: 'get'
  })
}

// 初始化系统
export const initSystem = (data) => {
  return request({
    url: '/v1/public/init',
    method: 'post',
    data
  })
}

// 获取初始化进度
export const getInitProgress = () => {
  return request({
    url: '/v1/public/init-progress',
    method: 'get'
  })
}

// 获取公告
export const getAnnouncements = () => {
  return request({
    url: '/v1/public/announcements',
    method: 'get'
  })
}

// 获取统计数据
export const getStats = () => {
  return request({
    url: '/v1/public/stats',
    method: 'get'
  })
}

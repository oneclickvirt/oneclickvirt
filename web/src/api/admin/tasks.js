import request from '@/utils/request'

export const getAdminTasks = (params) => {
  return request({
    url: '/v1/admin/tasks',
    method: 'get',
    params
  })
}

export const forceStopTask = (data) => {
  return request({
    url: '/v1/admin/tasks/force-stop',
    method: 'post',
    data
  })
}

export const getTaskStats = () => {
  return request({
    url: '/v1/admin/tasks/stats',
    method: 'get'
  })
}

export const getTaskOverallStats = () => {
  return request({
    url: '/v1/admin/tasks/overall-stats',
    method: 'get'
  })
}

export const cancelUserTaskByAdmin = (taskId) => {
  return request({
    url: `/v1/admin/tasks/${taskId}/cancel`,
    method: 'post'
  })
}

export const getAdminTaskDetail = (taskId) => {
  return request({
    url: `/v1/admin/tasks/${taskId}`,
    method: 'get'
  })
}

export const getTaskPoolStatus = () => {
  return request({
    url: '/v1/admin/tasks/pool-status',
    method: 'get'
  })
}

export const updateTaskPoolStatus = (data) => {
  return request({
    url: '/v1/admin/tasks/pool-status',
    method: 'put',
    data
  })
}

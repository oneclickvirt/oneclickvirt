// Provider页面的工具函数
import { formatMemorySize, formatDiskSize } from '@/utils/unit-formatter'
import { getFlagEmoji } from '@/utils/countries'

// 格式化流量大小
export const formatTraffic = (sizeInMB) => {
  if (!sizeInMB || sizeInMB === 0) return '0B'
  
  const units = ['MB', 'GB', 'TB', 'PB']
  let size = sizeInMB
  let unitIndex = 0
  
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex++
  }
  
  return `${size.toFixed(unitIndex === 0 ? 0 : 1)}${units[unitIndex]}`
}

// 计算流量使用百分比
export const getTrafficPercentage = (used, max) => {
  if (!max || max === 0) return 0
  return Math.min(Math.round((used / max) * 100), 100)
}

// 获取流量进度条状态
export const getTrafficProgressStatus = (used, max) => {
  const percentage = getTrafficPercentage(used, max)
  if (percentage >= 90) return 'exception'
  if (percentage >= 80) return 'warning'
  return 'success'
}

// 计算资源使用百分比（适用于CPU、内存、磁盘）
export const getResourcePercentage = (allocated, total) => {
  if (!total || total === 0) return 0
  return Math.min(Math.round((allocated / total) * 100), 100)
}

// 获取资源进度条状态（适用于CPU、内存、磁盘）
export const getResourceProgressStatus = (allocated, total) => {
  const percentage = getResourcePercentage(allocated, total)
  if (percentage >= 95) return 'exception'
  if (percentage >= 85) return 'warning'
  return 'success'
}

// 格式化日期时间
export const formatDateTime = (dateTimeStr) => {
  if (!dateTimeStr) return '-'
  const date = new Date(dateTimeStr)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

// 检查是否过期
export const isExpired = (dateTimeStr) => {
  if (!dateTimeStr) return false
  return new Date(dateTimeStr) < new Date()
}

// 检查是否即将过期（7天内）
export const isNearExpiry = (dateTimeStr) => {
  if (!dateTimeStr) return false
  const expiryDate = new Date(dateTimeStr)
  const now = new Date()
  const diffDays = (expiryDate - now) / (1000 * 60 * 60 * 24)
  return diffDays <= 7 && diffDays > 0
}

// 获取状态类型（用于el-tag的type属性）
export const getStatusType = (status) => {
  switch (status) {
    case 'online':
      return 'success'
    case 'offline':
      return 'danger'
    case 'unknown':
    default:
      return 'info'
  }
}

// 获取状态文本
export const getStatusText = (status) => {
  switch (status) {
    case 'online':
      return '在线'
    case 'offline':
      return '离线'
    case 'unknown':
    default:
      return '未知'
  }
}

// 获取等级标签类型
export const getLevelTagType = (level) => {
  const levelColors = {
    1: 'info',
    2: 'success',
    3: 'warning',
    4: 'danger',
    5: 'primary'
  }
  return levelColors[level] || 'info'
}

// 计算配额使用百分比
export const getQuotaPercentage = (current, max) => {
  if (!max || max === 0) return 0
  return Math.min(Math.round((current / max) * 100), 100)
}

// 获取配额进度条状态
export const getQuotaProgressStatus = (current, max) => {
  const percentage = getQuotaPercentage(current, max)
  if (percentage >= 100) return 'exception'
  if (percentage >= 90) return 'warning'
  return 'success'
}

// 导出常用工具函数
export {
  formatMemorySize,
  formatDiskSize,
  getFlagEmoji
}

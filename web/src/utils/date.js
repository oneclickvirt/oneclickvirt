import i18n from '@/i18n'
const t = (...args) => i18n.global.t(...args)

// 日期格式化工具函数
export function formatDate(date, format = 'YYYY-MM-DD HH:mm:ss') {
  if (!date) return ''
  
  const d = new Date(date)
  if (isNaN(d.getTime())) return ''
  
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  const hours = String(d.getHours()).padStart(2, '0')
  const minutes = String(d.getMinutes()).padStart(2, '0')
  const seconds = String(d.getSeconds()).padStart(2, '0')
  
  return format
    .replace('YYYY', year)
    .replace('MM', month)
    .replace('DD', day)
    .replace('HH', hours)
    .replace('mm', minutes)
    .replace('ss', seconds)
}

// 相对时间格式化
export function formatRelativeTime(date) {
  if (!date) return ''
  
  const d = new Date(date)
  if (isNaN(d.getTime())) return ''
  
  const now = new Date()
  const diff = now - d
  const seconds = Math.floor(diff / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)
  
  if (seconds < 60) {
    return t('common.justNow')
  } else if (minutes < 60) {
    return t('common.minutesAgo', { n: minutes })
  } else if (hours < 24) {
    return t('common.hoursAgo', { n: hours })
  } else if (days < 7) {
    return t('common.daysAgo', { n: days })
  } else {
    return formatDate(date, 'YYYY-MM-DD')
  }
}

// 格式化时间段
export function formatDuration(ms) {
  if (!ms) return t('common.zeroDuration')
  
  const seconds = Math.floor(ms / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)
  
  if (days > 0) {
    return t('common.durationDaysHours', { days, hours: hours % 24 })
  } else if (hours > 0) {
    return t('common.durationHoursMinutes', { hours, minutes: minutes % 60 })
  } else if (minutes > 0) {
    return t('common.durationMinutesSeconds', { minutes, seconds: seconds % 60 })
  } else {
    return t('common.durationSeconds', { n: seconds })
  }
}

// 检查日期是否过期
export function isExpired(date) {
  if (!date) return false
  const d = new Date(date)
  return d < new Date()
}

// 计算距离过期还有多少天
export function getDaysUntilExpiry(date) {
  if (!date) return null
  const d = new Date(date)
  const now = new Date()
  const diff = d - now
  return Math.ceil(diff / (1000 * 60 * 60 * 24))
}

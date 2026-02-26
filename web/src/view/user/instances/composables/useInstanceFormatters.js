// 实例详情页 - 纯格式化工具函数
import { useI18n } from 'vue-i18n'

export function useInstanceFormatters() {
  const { t } = useI18n()

  const getNetworkTypeFromLegacy = (ipv4MappingType, hasIPv6) => {
    if (ipv4MappingType === 'nat') {
      return hasIPv6 ? 'nat_ipv4_ipv6' : 'nat_ipv4'
    } else if (ipv4MappingType === 'dedicated') {
      return hasIPv6 ? 'dedicated_ipv4_ipv6' : 'dedicated_ipv4'
    } else if (ipv4MappingType === 'ipv6_only') {
      return 'ipv6_only'
    }
    return 'nat_ipv4'
  }

  const getNetworkTypeDisplayName = (networkType) => {
    const typeNames = {
      'nat_ipv4': 'NAT IPv4',
      'nat_ipv4_ipv6': `NAT IPv4 + ${t('user.apply.networkConfig.dedicatedIPv6')}`,
      'dedicated_ipv4': t('user.apply.networkConfig.dedicatedIPv4'),
      'dedicated_ipv4_ipv6': `${t('user.apply.networkConfig.dedicatedIPv4')} + ${t('user.apply.networkConfig.dedicatedIPv6')}`,
      'ipv6_only': t('user.apply.networkConfig.ipv6Only')
    }
    return typeNames[networkType] || t('user.instanceDetail.unknownType')
  }

  const getNetworkTypeTagType = (networkType) => {
    const tagTypes = {
      'nat_ipv4': 'primary',
      'nat_ipv4_ipv6': 'success',
      'dedicated_ipv4': 'warning',
      'dedicated_ipv4_ipv6': 'success',
      'ipv6_only': 'info'
    }
    return tagTypes[networkType] || 'default'
  }

  const getProviderTypeName = (type) => {
    const names = { docker: 'Docker', lxd: 'LXD', incus: 'Incus', proxmox: 'Proxmox' }
    return names[type] || type
  }

  const getProviderTypeColor = (type) => {
    const colors = { docker: 'info', lxd: 'success', incus: 'warning', proxmox: '' }
    return colors[type] || ''
  }

  const getTaskTitle = (task) => {
    const taskTypes = {
      create: '创建实例',
      delete: '删除实例',
      start: '启动实例',
      stop: '停止实例',
      restart: '重启实例',
      reset: '重置实例',
      reset_password: '重置密码'
    }
    return taskTypes[task.taskType] || '实例操作'
  }

  const getTaskTypeText = (taskType) => {
    const taskTypes = {
      create: '创建',
      delete: '删除',
      start: '启动',
      stop: '停止',
      restart: '重启',
      reset: '重置',
      reset_password: '重置密码',
      create_redemption_instance: '兑换开设'
    }
    return taskTypes[taskType] || '处理'
  }

  const getTaskAlertType = (status) => {
    const types = {
      pending: 'info',
      processing: 'warning',
      running: 'warning',
      completed: 'success',
      failed: 'error',
      cancelled: 'info'
    }
    return types[status] || 'info'
  }

  const getStatusType = (status) => {
    const statusMap = {
      'running': 'success',
      'stopped': 'info',
      'paused': 'warning',
      'starting': 'warning',
      'stopping': 'warning',
      'restarting': 'warning',
      'resetting': 'warning',
      'processing': 'warning',
      'unavailable': 'danger',
      'error': 'danger',
      'failed': 'danger'
    }
    return statusMap[status] || 'info'
  }

  const getStatusText = (status) => {
    const statusMap = {
      'running': t('user.instanceDetail.statusRunning'),
      'stopped': t('user.instanceDetail.statusStopped'),
      'paused': t('user.instanceDetail.statusPaused'),
      'starting': t('user.instanceDetail.statusStarting'),
      'stopping': t('user.instanceDetail.statusStopping'),
      'restarting': t('user.instanceDetail.statusRestarting'),
      'resetting': t('user.instanceDetail.statusResetting'),
      'processing': t('user.instanceDetail.statusProcessing'),
      'unavailable': t('user.instanceDetail.statusUnavailable'),
      'error': t('user.instanceDetail.statusError'),
      'failed': t('user.instanceDetail.statusFailed')
    }
    return statusMap[status] || status
  }

  const getTrafficProgressColor = (percentage) => {
    if (percentage < 70) return '#67c23a'
    if (percentage < 90) return '#e6a23c'
    return '#f56c6c'
  }

  const formatTraffic = (mb) => {
    if (!mb || mb === 0) return '0 MB'
    if (mb < 1024) return `${mb} MB`
    if (mb < 1024 * 1024) return `${(mb / 1024).toFixed(1)} GB`
    return `${(mb / (1024 * 1024)).toFixed(1)} TB`
  }

  const formatDate = (dateString) => {
    if (!dateString) return '暂无'
    return new Date(dateString).toLocaleString('zh-CN')
  }

  // monitoring 作为参数传入以避免跨 composable 依赖
  const getTrafficLimitTitle = (monitoring) => {
    const limitType = monitoring?.trafficData?.limitType
    switch (limitType) {
      case 'user':   return t('user.instanceDetail.userTrafficWarning')
      case 'provider': return t('user.instanceDetail.trafficWarning')
      case 'both':   return t('user.instanceDetail.dualTrafficWarning')
      default:       return t('user.instanceDetail.trafficWarning')
    }
  }

  const getTrafficLimitType = (monitoring) => {
    const limitType = monitoring?.trafficData?.limitType
    switch (limitType) {
      case 'provider':
      case 'both': return 'error'
      case 'user': return 'warning'
      default:     return 'warning'
    }
  }

  return {
    getNetworkTypeFromLegacy,
    getNetworkTypeDisplayName,
    getNetworkTypeTagType,
    getProviderTypeName,
    getProviderTypeColor,
    getTaskTitle,
    getTaskTypeText,
    getTaskAlertType,
    getStatusType,
    getStatusText,
    getTrafficProgressColor,
    formatTraffic,
    formatDate,
    getTrafficLimitTitle,
    getTrafficLimitType
  }
}

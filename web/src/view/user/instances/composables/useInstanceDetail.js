// 实例详情页 - 状态管理与数据加载
import { ref, reactive, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import {
  getUserInstanceDetail,
  getInstanceMonitoring,
  getUserInstancePorts,
  getUserInstanceTypePermissions,
  getSharedInstanceDetail,
  getSharedInstanceMonitoring,
  getSharedInstancePorts
} from '@/api/user'

export function useInstanceDetail(shareToken = '') {
  const route = useRoute()
  const router = useRouter()
  const { t } = useI18n()

  const getShareToken = () => {
    if (typeof shareToken === 'string') return shareToken
    return shareToken?.value || ''
  }

  const loading = ref(false)
  const portMappings = ref([])
  const trafficChartRef = ref(null)

  const instanceTypePermissions = ref({
    canCreateContainer: false,
    canCreateVM: false,
    canDeleteInstance: false,
    canResetInstance: false,
    canDeleteContainer: false,
    canDeleteVM: false,
    canResetContainer: false,
    canResetVM: false
  })

  const instance = ref({
    id: '',
    name: '',
    type: '',
    status: '',
    providerName: '',
    osType: '',
    cpu: 0,
    memory: 0,
    disk: 0,
    bandwidth: 0,
    privateIP: '',
    publicIP: '',
    ipv6Address: '',
    publicIPv6: '',
    sshPort: '',
    username: '',
    password: '',
    createdAt: '',
    expiresAt: '',
    portRangeStart: 0,
    portRangeEnd: 0
  })

  const monitoring = reactive({
    trafficData: {
      currentMonth: 0,
      totalLimit: 102400,
      usagePercent: 0,
      isLimited: false,
      history: []
    }
  })

  const updateInstancePermissions = () => {
    if (instance.value.instance_type === 'vm') {
      instanceTypePermissions.value.canDeleteInstance = instanceTypePermissions.value.canDeleteVM
      instanceTypePermissions.value.canResetInstance = instanceTypePermissions.value.canResetVM
    } else {
      instanceTypePermissions.value.canDeleteInstance = instanceTypePermissions.value.canDeleteContainer
      instanceTypePermissions.value.canResetInstance = instanceTypePermissions.value.canResetContainer
    }
  }

  const loadInstanceDetail = async (skipPermissionUpdate = false) => {
    const token = getShareToken()
    if (!token && (!route.params.id || route.params.id === 'undefined')) {
      console.error('实例ID无效，返回实例列表')
      ElMessage.error(t('user.instances.instanceInvalid'))
      router.push('/user/instances')
      return false
    }

    try {
      loading.value = true
      const response = token
        ? await getSharedInstanceDetail(token)
        : await getUserInstanceDetail(route.params.id)
      if (response.code === 200) {
        const data = response.data
        if (data.type && !data.instance_type) {
          data.instance_type = data.type
        }
        Object.assign(instance.value, data)
        if (!skipPermissionUpdate) {
          updateInstancePermissions()
        }
        return true
      }
      return false
    } catch (error) {
      console.error('获取实例详情失败:', error)
      ElMessage.error(error?.fullMessage || error?.userMessage || error?.details || error?.message || t('user.instanceDetail.getDetailFailed'))
      if (token) {
        router.push('/home')
      } else {
        router.push('/user/instances')
      }
      return false
    } finally {
      loading.value = false
    }
  }

  const refreshPortMappings = async () => {
    const token = getShareToken()
    if (!token && !route.params.id) return

    try {
      const response = token
        ? await getSharedInstancePorts(token)
        : await getUserInstancePorts(route.params.id)
      if (response.code === 200) {
        portMappings.value = response.data.list || []
        if (response.data.publicIP) {
          instance.value.publicIP = response.data.publicIP
        }
        if (response.data.instance) {
          instance.value.username = response.data.instance.username || instance.value.username
        }
      }
    } catch (error) {
      console.error('获取端口映射失败:', error)
    }
  }

  const refreshMonitoring = async () => {
    const token = getShareToken()
    if (!token && (!route.params.id || route.params.id === 'undefined')) {
      console.warn('实例ID无效，跳过监控数据获取')
      return
    }

    try {
      const response = token
        ? await getSharedInstanceMonitoring(token)
        : await getInstanceMonitoring(route.params.id)
      if (response.code === 200) {
        Object.assign(monitoring, response.data)
        if (monitoring.trafficData?.isLimited) {
          ElMessage.warning(t('user.instanceDetail.trafficLimitWarning'))
        }
      }
    } catch (error) {
      console.error('获取监控数据失败:', error)
      monitoring.trafficData = {
        currentMonth: 0,
        totalLimit: 102400,
        usagePercent: 0,
        isLimited: false,
        history: []
      }
      ElMessage.error(error?.fullMessage || error?.userMessage || error?.details || error?.message || t('user.instanceDetail.getMonitoringFailed'))
    }

    if (trafficChartRef.value && trafficChartRef.value.refresh) {
      trafficChartRef.value.refresh()
    }
  }

  const loadInstanceTypePermissions = async () => {
    if (getShareToken()) {
      instanceTypePermissions.value = {
        canCreateContainer: false,
        canCreateVM: false,
        canDeleteContainer: true,
        canDeleteVM: true,
        canResetContainer: true,
        canResetVM: true,
        canDeleteInstance: true,
        canResetInstance: true
      }
      return true
    }
    try {
      const response = await getUserInstanceTypePermissions()
      if (response.code === 200) {
        const data = response.data || {}
        instanceTypePermissions.value = {
          canCreateContainer: data.canCreateContainer || false,
          canCreateVM: data.canCreateVM || false,
          canDeleteContainer: data.canDeleteContainer || false,
          canDeleteVM: data.canDeleteVM || false,
          canResetContainer: data.canResetContainer || false,
          canResetVM: data.canResetVM || false,
          canDeleteInstance: false,
          canResetInstance: false
        }
        return true
      }
      return false
    } catch (error) {
      console.error('获取实例类型权限失败:', error)
      instanceTypePermissions.value = {
        canCreateContainer: false,
        canCreateVM: false,
        canDeleteInstance: false,
        canResetInstance: false,
        canDeleteContainer: false,
        canDeleteVM: false,
        canResetContainer: false,
        canResetVM: false
      }
      return false
    }
  }

  // 定时检测分享令牌有效期，过期时自动刷新页面避免用户看到缓存的过期内容
  let shareTokenTimer = null
  const SHARE_TOKEN_CHECK_INTERVAL = 30000 // 30 秒

  const startShareTokenMonitor = () => {
    const token = getShareToken()
    if (!token) return

    // 清除已有定时器
    if (shareTokenTimer) clearInterval(shareTokenTimer)

    shareTokenTimer = setInterval(async () => {
      try {
        await getSharedInstanceDetail(token)
        // 请求成功，令牌仍然有效
      } catch (error) {
        const msg = error?.message || error?.details || ''
        if (msg.includes('过期') || msg.includes('无效') || error?.code === 401) {
          clearInterval(shareTokenTimer)
          shareTokenTimer = null
          ElMessage.warning(t('user.instanceDetail.shareLinkExpired'))
          router.push('/home')
        }
      }
    }, SHARE_TOKEN_CHECK_INTERVAL)

    // 组件卸载时清理定时器
    onUnmounted(() => {
      if (shareTokenTimer) {
        clearInterval(shareTokenTimer)
        shareTokenTimer = null
      }
    })
  }

  return {
    loading,
    portMappings,
    trafficChartRef,
    instanceTypePermissions,
    instance,
    monitoring,
    updateInstancePermissions,
    loadInstanceDetail,
    refreshPortMappings,
    refreshMonitoring,
    loadInstanceTypePermissions,
    startShareTokenMonitor
  }
}

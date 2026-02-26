// 实例详情页 - 状态管理与数据加载
import { ref, reactive } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import {
  getUserInstanceDetail,
  getInstanceMonitoring,
  getUserInstancePorts,
  getUserInstanceTypePermissions
} from '@/api/user'

export function useInstanceDetail() {
  const route = useRoute()
  const router = useRouter()
  const { t } = useI18n()

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
    if (!route.params.id || route.params.id === 'undefined') {
      console.error('实例ID无效，返回实例列表')
      ElMessage.error(t('user.instances.instanceInvalid'))
      router.push('/user/instances')
      return false
    }

    try {
      loading.value = true
      const response = await getUserInstanceDetail(route.params.id)
      if (response.code === 0 || response.code === 200) {
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
      ElMessage.error(t('user.instanceDetail.getDetailFailed'))
      router.back()
      return false
    } finally {
      loading.value = false
    }
  }

  const refreshPortMappings = async () => {
    if (!route.params.id) return

    try {
      const response = await getUserInstancePorts(route.params.id)
      if (response.code === 0 || response.code === 200) {
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
    if (!route.params.id || route.params.id === 'undefined') {
      console.warn('实例ID无效，跳过监控数据获取')
      return
    }

    try {
      const response = await getInstanceMonitoring(route.params.id)
      if (response.code === 0 || response.code === 200) {
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
      ElMessage.error(t('user.instanceDetail.getMonitoringFailed'))
    }

    if (trafficChartRef.value && trafficChartRef.value.refresh) {
      trafficChartRef.value.refresh()
    }
  }

  const loadInstanceTypePermissions = async () => {
    try {
      const response = await getUserInstanceTypePermissions()
      if (response.code === 0 || response.code === 200) {
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
    loadInstanceTypePermissions
  }
}

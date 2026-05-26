import { ref, reactive, onMounted, onActivated, onUnmounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { getUserInstances } from '@/api/user'

export function useUserInstances() {
  const { t, locale } = useI18n()
  const router = useRouter()
  const route = useRoute()

  const loading = ref(false)
  const instances = ref([])
  const total = ref(0)

  // 流量详情对话框
  const showTrafficDialog = ref(false)
  const selectedInstanceForTraffic = ref(null)

  const filterForm = reactive({
    name: '',
    type: '',
    status: '',
    providerName: ''
  })

  const pagination = reactive({
    page: 1,
    pageSize: 10
  })

  // 处理搜索
  const handleSearch = () => {
    pagination.page = 1
    loadInstances(true)
  }

  // 获取实例列表
  const loadInstances = async (showSuccessMsg = false) => {
    try {
      loading.value = true
      const params = {
        page: pagination.page,
        pageSize: pagination.pageSize,
        ...filterForm
      }

      const response = await getUserInstances(params)
      instances.value = response.data.list || []
      total.value = response.data.total || 0
      // 只有在明确刷新时才显示成功提示
      if (showSuccessMsg) {
        ElMessage.success(t('user.instances.refreshSuccess', { count: total.value }))
      }
    } catch (error) {
      console.error('获取实例列表失败:', error)
      instances.value = []
      total.value = 0
      ElMessage.error(error?.message || t('user.instances.loadFailed'))
    } finally {
      loading.value = false
    }
  }

  // 重置筛选
  const resetFilter = () => {
    Object.assign(filterForm, {
      name: '',
      type: '',
      status: '',
      providerName: ''
    })
    pagination.page = 1
    loadInstances(true)
  }

  // 获取状态类型
  const getStatusType = (status) => {
    const statusMap = {
      'running': 'success',
      'stopped': 'info',
      'paused': 'warning',
      'creating': 'warning',
      'starting': 'warning',
      'stopping': 'warning',
      'restarting': 'warning',
      'resetting': 'warning',
      'unavailable': 'danger',
      'error': 'danger',
      'failed': 'danger'
    }
    return statusMap[status] || 'info'
  }

  // 获取状态文本
  const getStatusText = (status) => {
    const statusMap = {
      'running': t('user.instances.statusRunning'),
      'stopped': t('user.instances.statusStopped'),
      'paused': t('user.instances.statusPaused'),
      'creating': t('user.instances.statusCreating'),
      'starting': t('user.instances.statusStarting'),
      'stopping': t('user.instances.statusStopping'),
      'restarting': t('user.instances.statusRestarting'),
      'resetting': t('user.instances.statusResetting'),
      'unavailable': t('user.instances.statusUnavailable'),
      'error': t('user.instances.statusError'),
      'failed': t('user.instances.statusFailed')
    }
    return statusMap[status] || status
  }

  // 获取Provider类型名称
  const getProviderTypeName = (type) => {
    const names = {
      docker: 'Docker',
      lxd: 'LXD',
      incus: 'Incus',
      proxmox: 'Proxmox',
      podman: 'Podman',
      containerd: 'Containerd',
      qemu: 'QEMU/KVM',
      kubevirt: 'KubeVirt'
    }
    return names[type] || type
  }

  // 获取Provider类型颜色
  const getProviderTypeColor = (type) => {
    const colors = {
      docker: 'info',
      lxd: 'success',
      incus: 'warning',
      proxmox: '',
      podman: 'info',
      containerd: 'info',
      qemu: 'danger',
      kubevirt: 'danger'
    }
    return colors[type] || ''
  }

  // 格式化日期
  const formatDate = (dateString) => {
    const localeCode = locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'
    return new Date(dateString).toLocaleString(localeCode)
  }

  // 查看实例详情
  const viewInstanceDetail = (instance) => {
    if (!instance || !instance.id) {
      console.error('实例对象无效:', instance)
      ElMessage.error(t('user.instances.instanceInvalid'))
      return
    }

    // 只允许运行中、停止中、已停止状态进入详情页面
    const allowedStatuses = ['running', 'stopped', 'stopping']
    if (!allowedStatuses.includes(instance.status)) {
      const statusText = getStatusText(instance.status)
      ElMessage.warning(t('user.instances.cannotViewDetail', { status: statusText }))
      return
    }

    router.push(`/user/instances/${instance.id}`)
  }

  // 显示实例流量详情
  const showTrafficDetail = (instance) => {
    if (!instance || !instance.id) {
      console.error('实例对象无效:', instance)
      ElMessage.error(t('user.instances.instanceInvalid'))
      return
    }
    selectedInstanceForTraffic.value = instance
    showTrafficDialog.value = true
  }

  // 监听路由变化，确保页面切换时重新加载数据
  watch(() => route.path, (newPath, oldPath) => {
    if (newPath === '/user/instances' && oldPath !== newPath) {
      loadInstances()
    }
  }, { immediate: false })

  // 监听自定义导航事件
  const handleRouterNavigation = (event) => {
    if (event.detail && event.detail.path === '/user/instances') {
      loadInstances()
    }
  }

  // 处理强制刷新事件
  const handleForceRefresh = async (event) => {
    if (event.detail && event.detail.path === '/user/instances') {
      loading.value = true
      try {
        await loadInstances()
      } catch (error) {
        console.error('获取实例列表失败:', error)
      } finally {
        loading.value = false
      }
    }
  }

  onMounted(async () => {
    // 自定义导航事件监听器
    window.addEventListener('router-navigation', handleRouterNavigation)
    // 强制页面刷新监听器
    window.addEventListener('force-page-refresh', handleForceRefresh)

    loading.value = true
    try {
      await loadInstances()
    } catch (error) {
      console.error('获取实例列表失败:', error)
    } finally {
      loading.value = false
    }
  })

  // 使用 onActivated 确保每次页面激活时都重新加载数据
  onActivated(async () => {
    loading.value = true
    try {
      await loadInstances()
    } catch (error) {
      console.error('获取实例列表失败:', error)
    } finally {
      loading.value = false
    }
  })

  onUnmounted(() => {
    // 移除事件监听器
    window.removeEventListener('router-navigation', handleRouterNavigation)
    window.removeEventListener('force-page-refresh', handleForceRefresh)
  })

  return {
    loading,
    instances,
    total,
    showTrafficDialog,
    selectedInstanceForTraffic,
    filterForm,
    pagination,
    handleSearch,
    loadInstances,
    resetFilter,
    getStatusType,
    getStatusText,
    getProviderTypeName,
    getProviderTypeColor,
    formatDate,
    viewInstanceDetail,
    showTrafficDetail
  }
}

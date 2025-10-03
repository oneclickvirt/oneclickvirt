<template>
  <div class="user-instances">
    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-container"
    >
      <el-loading-directive />
      <div class="loading-text">
        加载实例数据中...
      </div>
    </div>
    
    <!-- 主要内容 -->
    <div v-else>
      <div class="page-header">
        <h1>我的实例</h1>
        <p>管理您的虚拟机和容器实例</p>
      </div>

      <!-- 筛选和搜索 -->
      <div class="filter-section">
        <el-form
          :inline="true"
          :model="filterForm"
        >
          <el-form-item label="类型">
            <el-select
              v-model="filterForm.type"
              placeholder="选择类型"
              clearable
            >
              <el-option
                label="全部"
                value=""
              />
              <el-option
                label="虚拟机"
                value="vm"
              />
              <el-option
                label="容器"
                value="container"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="状态">
            <el-select
              v-model="filterForm.status"
              placeholder="选择状态"
              clearable
            >
              <el-option
                label="全部"
                value=""
              />
              <el-option
                label="运行中"
                value="running"
              />
              <el-option
                label="已停止"
                value="stopped"
              />
              <el-option
                label="暂停"
                value="paused"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="服务器">
            <el-select
              v-model="filterForm.providerId"
              placeholder="选择服务器"
              clearable
            >
              <el-option
                label="全部"
                value=""
              />
              <el-option 
                v-for="provider in providers" 
                :key="provider.id" 
                :label="provider.name" 
                :value="provider.id" 
              />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              @click="() => loadInstances(true)"
            >
              筛选
            </el-button>
            <el-button @click="resetFilter">
              重置
            </el-button>
          </el-form-item>
        </el-form>
      </div>

      <!-- 实例列表 -->
      <div class="instances-grid">
        <div 
          v-for="instance in instances" 
          :key="instance.id" 
          class="instance-card"
          @click="viewInstanceDetail(instance)"
        >
          <div class="instance-header">
            <div class="instance-info">
              <h3>{{ instance.name }}</h3>
              <div class="instance-type">
                <el-tag :type="instance.instance_type === 'vm' ? 'primary' : 'success'">
                  {{ instance.instance_type === 'vm' ? '虚拟机' : '容器' }}
                </el-tag>
              </div>
            </div>
            <div class="instance-status">
              <el-tag 
                :type="getStatusType(instance.status)"
                effect="dark"
              >
                {{ getStatusText(instance.status) }}
              </el-tag>
            </div>
          </div>

          <div class="instance-details">
            <div class="detail-item">
              <span class="label">服务器:</span>
              <span class="value">{{ instance.providerName }}</span>
            </div>
            <div class="detail-item">
              <span class="label">配置:</span>
              <span class="value">{{ instance.cpu }}核 / {{ formatMemorySize(instance.memory) }} / {{ formatDiskSize(instance.disk) }}</span>
            </div>
            <div class="detail-item">
              <span class="label">带宽:</span>
              <span class="value">{{ instance.bandwidth || 100 }}Mbps</span>
            </div>
            <div class="detail-item">
              <span class="label">系统:</span>
              <span class="value">{{ instance.osType }}</span>
            </div>
            <div class="detail-item">
              <span class="label">创建时间:</span>
              <span class="value">{{ formatDate(instance.createdAt) }}</span>
            </div>
            <!-- 端口映射信息 -->
            <div
              v-if="instance.portMappings && instance.portMappings.length > 0"
              class="detail-item port-info"
            >
              <span class="label">端口映射:</span>
              <div class="port-mappings">
                <div class="public-ip">
                  <el-tag
                    type="info"
                    size="small"
                  >
                    公网IP: {{ instance.publicIP || '未分配' }}
                  </el-tag>
                </div>
                <!-- 普通用户不显示端口区间 -->
                <div class="port-list">
                  <el-tag 
                    v-for="port in instance.portMappings.slice(0, 3)" 
                    :key="port.id"
                    size="small"
                    effect="plain"
                    class="port-tag"
                    :type="port.isSSH ? 'warning' : 'primary'"
                  >
                    <span v-if="port.isSSH">SSH: {{ port.publicPort }}</span>
                    <span v-else>{{ port.publicPort }}:{{ port.internalPort }}/{{ port.protocol }}</span>
                  </el-tag>
                  <el-tag 
                    v-if="instance.portMappings.length > 3"
                    size="small"
                    type="info"
                    effect="plain"
                  >
                    +{{ instance.portMappings.length - 3 }}个
                  </el-tag>
                </div>
              </div>
            </div>
          </div>

          <!-- 实例操作按钮 -->
          <div class="instance-actions" @click.stop>
            <el-button
              size="small"
              type="primary"
              @click="showTrafficDetail(instance)"
            >
              <el-icon><TrendCharts /></el-icon>
              流量详情
            </el-button>
            <el-button
              size="small"
              @click="viewInstanceDetail(instance)"
            >
              <el-icon><View /></el-icon>
              查看详情
            </el-button>
          </div>
        </div>
      </div>

      <!-- 空状态 -->
      <el-empty
        v-if="instances.length === 0 && !loading"
        description="您还没有任何实例"
      >
        <el-button
          type="primary"
          @click="$router.push('/user/apply')"
        >
          立即申请
        </el-button>
      </el-empty>

      <!-- 分页 -->
      <div
        v-if="total > 0"
        class="pagination"
      >
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.pageSize"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="loadInstances"
          @current-change="loadInstances"
        />
      </div>

      <!-- 加载状态 -->
      <div
        v-if="loading"
        class="loading-container"
      >
        <el-skeleton
          :rows="5"
          animated
        />
      </div>
    </div> <!-- 结束主要内容区域 -->

    <!-- 流量详情对话框 -->
    <InstanceTrafficDetail
      v-model="showTrafficDialog"
      :instance-id="selectedInstanceForTraffic?.id"
      :instance-name="selectedInstanceForTraffic?.name"
    />
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, watch, onActivated, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { TrendCharts, View } from '@element-plus/icons-vue'
import { getUserInstances, getAvailableProviders } from '@/api/user'
import { formatDiskSize, formatMemorySize } from '@/utils/unit-formatter'
import InstanceTrafficDetail from '@/components/InstanceTrafficDetail.vue'

const router = useRouter()
const route = useRoute()

const loading = ref(false)
const instances = ref([])
const providers = ref([])
const total = ref(0)

// 流量详情对话框
const showTrafficDialog = ref(false)
const selectedInstanceForTraffic = ref(null)



const filterForm = reactive({
  type: '',
  status: '',
  providerId: ''
})

const pagination = reactive({
  page: 1,
  pageSize: 10
})

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
    if (response.code === 0 || response.code === 200) {
      instances.value = response.data.list || []
      total.value = response.data.total || 0
      // 只有在明确刷新时才显示成功提示
      if (showSuccessMsg) {
        ElMessage.success(`已刷新，共 ${total.value} 个实例`)
      }
    } else {
      instances.value = []
      total.value = 0
      ElMessage.error(response.message || '获取实例列表失败')
    }
  } catch (error) {
    console.error('获取实例列表失败:', error)
    instances.value = []
    total.value = 0
    ElMessage.error('获取实例列表失败，请检查网络连接')
  } finally {
    loading.value = false
  }
}

// 获取服务器列表
const loadProviders = async () => {
  try {
    const response = await getAvailableProviders()
    if (response.code === 200) {
      providers.value = response.data.list || []
    } else {
      providers.value = []
    }
  } catch (error) {
    console.error('获取可用节点失败:', error)
    providers.value = []
  }
}



// 重置筛选
const resetFilter = () => {
  Object.assign(filterForm, {
    type: '',
    status: '',
    providerId: ''
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
    'error': 'danger',
    'failed': 'danger'
  }
  return statusMap[status] || 'info'
}

// 获取状态文本
const getStatusText = (status) => {
  const statusMap = {
    'running': '运行中',
    'stopped': '已停止', 
    'paused': '暂停',
    'creating': '创建中',
    'error': '错误',
    'failed': '创建失败'
  }
  return statusMap[status] || status
}

// 格式化日期
const formatDate = (dateString) => {
  return new Date(dateString).toLocaleString('zh-CN')
}

// 查看实例详情
const viewInstanceDetail = (instance) => {
  if (!instance || !instance.id) {
    console.error('实例对象无效:', instance)
    ElMessage.error('实例信息无效')
    return
  }
  
  // 只允许运行中、停止中、已停止状态进入详情页面
  const allowedStatuses = ['running', 'stopped', 'stopping']
  if (!allowedStatuses.includes(instance.status)) {
    const statusText = getStatusText(instance.status)
    ElMessage.warning(`实例当前状态为"${statusText}"，无法查看详情。请等待实例进入运行或停止状态。`)
    return
  }
  
  router.push(`/user/instances/${instance.id}`)
}

// 显示实例流量详情
const showTrafficDetail = (instance) => {
  if (!instance || !instance.id) {
    console.error('实例对象无效:', instance)
    ElMessage.error('实例信息无效')
    return
  }
  selectedInstanceForTraffic.value = instance
  showTrafficDialog.value = true
}

// 监听路由变化，确保页面切换时重新加载数据
watch(() => route.path, (newPath, oldPath) => {
  if (newPath === '/user/instances' && oldPath !== newPath) {
    loadInstances()
    loadProviders()
  }
}, { immediate: false })

// 监听自定义导航事件
const handleRouterNavigation = (event) => {
  if (event.detail && event.detail.path === '/user/instances') {
    loadInstances()
    loadProviders()
  }
}

onMounted(async () => {
  // 添加自定义导航事件监听器
  window.addEventListener('router-navigation', handleRouterNavigation)
  // 添加强制页面刷新监听器
  window.addEventListener('force-page-refresh', handleForceRefresh)
  
  loading.value = true
  try {
    // 使用Promise.allSettled确保即使某些API失败，页面也能正常显示
    const results = await Promise.allSettled([
      loadInstances(),
      loadProviders()
    ])
    
    results.forEach((result, index) => {
      if (result.status === 'rejected') {
        const apiNames = ['获取实例列表', '获取节点列表']
        console.error(`${apiNames[index]}失败:`, result.reason)
      }
    })
  } finally {
    loading.value = false
  }
})

// 使用 onActivated 确保每次页面激活时都重新加载数据
onActivated(async () => {
  loading.value = true
  try {
    await Promise.allSettled([
      loadInstances(),
      loadProviders()
    ])
  } finally {
    loading.value = false
  }
})

// 处理强制刷新事件
const handleForceRefresh = async (event) => {
  if (event.detail && event.detail.path === '/user/instances') {
    loading.value = true
    try {
      await Promise.allSettled([
        loadInstances(),
        loadProviders()
      ])
    } finally {
      loading.value = false
    }
  }
}

onUnmounted(() => {
  // 移除事件监听器
  window.removeEventListener('router-navigation', handleRouterNavigation)
  window.removeEventListener('force-page-refresh', handleForceRefresh)
})
</script>

<style scoped>
.user-instances {
  padding: 24px;
}

.loading-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 400px;
  color: #666;
}

.loading-text {
  margin-top: 16px;
  font-size: 14px;
}

.page-header {
  margin-bottom: 24px;
}

.page-header h1 {
  margin: 0 0 8px 0;
  font-size: 24px;
  font-weight: 600;
  color: #1f2937;
}

.page-header p {
  margin: 0;
  color: #6b7280;
}

.filter-section {
  background: white;
  padding: 16px;
  border-radius: 8px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  margin-bottom: 24px;
}

.instances-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(400px, 1fr));
  gap: 20px;
  margin-bottom: 24px;
}

.instance-card {
  background: white;
  border: 1px solid #e5e7eb;
  border-radius: 12px;
  padding: 20px;
  cursor: pointer;
  transition: all 0.3s ease;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.instance-card:hover {
  border-color: #10b981;
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.15);
  transform: translateY(-2px);
}

.instance-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
}

.instance-info h3 {
  margin: 0 0 8px 0;
  font-size: 18px;
  font-weight: 600;
  color: #1f2937;
}

.instance-details {
  margin-bottom: 16px;
}

.detail-item {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 8px;
  font-size: 14px;
}

.detail-item.port-info {
  flex-direction: column;
  align-items: flex-start;
}

.detail-item .label {
  color: #6b7280;
  font-weight: 500;
  min-width: 80px;
}

.detail-item .value {
  color: #1f2937;
  text-align: right;
  flex: 1;
}

.port-mappings {
  margin-top: 8px;
  width: 100%;
}

.public-ip, .port-range, .ipv6-info {
  margin-bottom: 8px;
}

.port-list {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 4px;
}

.port-tag {
  margin: 2px;
  font-size: 12px;
}

.instance-details {
  margin-bottom: 16px;
}

.detail-item {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 8px;
  font-size: 14px;
}

.detail-item.port-info {
  flex-direction: column;
  align-items: flex-start;
}

.detail-item .label {
  color: #6b7280;
  font-weight: 500;
  min-width: 80px;
}

.detail-item .value {
  color: #1f2937;
  text-align: right;
  flex: 1;
}

.port-mappings {
  margin-top: 8px;
  width: 100%;
}

.public-ip, .port-range, .ipv6-info {
  margin-bottom: 8px;
}


.pagination {
  display: flex;
  justify-content: center;
  margin-top: 24px;
}

.loading-container {
  padding: 24px;
}

.instance-actions {
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: 12px;
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}

.instance-actions .el-button {
  font-size: 12px;
}
</style>

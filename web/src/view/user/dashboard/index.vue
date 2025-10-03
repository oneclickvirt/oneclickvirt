<template>
  <div class="user-dashboard">
    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-container"
    >
      <el-loading-directive />
      <div class="loading-text">
        加载中...
      </div>
    </div>
    
    <!-- 主要内容 -->
    <div v-else>
      <div class="dashboard-header">
        <h1>欢迎回来，{{ userInfo?.nickname || userInfo?.username || '用户' }}！</h1>
        <p>这里是您的个人控制面板</p>
      </div>

      <!-- 用户等级信息 -->
      <div class="user-level-section">
        <el-card class="level-card">
          <template #header>
            <div class="card-header">
              <span>用户等级信息</span>
              <el-tag
                :type="getLevelTagType(userLimits.level)"
                size="large"
                effect="dark"
              >
                {{ getLevelText(userLimits.level) }}
              </el-tag>
            </div>
          </template>
          
          <div class="level-content">
            <div class="level-info">
              <div class="level-display">
                <div class="level-number">
                  {{ userLimits.level }}
                </div>
                <div class="level-description">
                  <h3>{{ getLevelText(userLimits.level) }}</h3>
                  <p>{{ getLevelDescription(userLimits.level) }}</p>
                </div>
              </div>
              
              <div class="level-benefits">
                <h4>等级权益</h4>
                <ul>
                  <li
                    v-for="benefit in getLevelBenefits(userLimits.level)"
                    :key="benefit"
                  >
                    {{ benefit }}
                  </li>
                </ul>
              </div>
            </div>
          </div>
        </el-card>
      </div>

      <!-- 资源限制信息 -->
      <div class="resource-limits-section">
        <el-card>
          <template #header>
            <div class="card-header">
              <span>资源配额限制</span>
              <el-button
                size="small"
                @click="loadUserLimits"
              >
                <el-icon><Refresh /></el-icon>
                刷新
              </el-button>
            </div>
          </template>
        
          <div class="limits-grid">
            <!-- 实例数量限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">实例数量</span>
                <span class="limit-usage">{{ userLimits.usedInstances }} / {{ userLimits.maxInstances }}</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedInstances, userLimits.maxInstances)"
                :color="getProgressColor(userLimits.usedInstances, userLimits.maxInstances)"
                :stroke-width="8"
              />
              <div class="limit-description">
                可创建的虚拟机和容器总数
              </div>
            </div>

            <!-- CPU核心限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">CPU核心</span>
                <span class="limit-usage">{{ userLimits.usedCpu }} / {{ userLimits.maxCpu }}核</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedCpu, userLimits.maxCpu)"
                :color="getProgressColor(userLimits.usedCpu, userLimits.maxCpu)"
                :stroke-width="8"
              />
              <div class="limit-description">
                所有实例CPU核心总数
              </div>
            </div>

            <!-- 内存限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">内存大小</span>
                <span class="limit-usage">{{ formatMemory(userLimits.usedMemory) }} / {{ formatMemory(userLimits.maxMemory) }}</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedMemory, userLimits.maxMemory)"
                :color="getProgressColor(userLimits.usedMemory, userLimits.maxMemory)"
                :stroke-width="8"
              />
              <div class="limit-description">
                所有实例内存总量
              </div>
            </div>

            <!-- 存储空间限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">存储空间</span>
                <span class="limit-usage">{{ formatStorage(userLimits.usedDisk) }} / {{ formatStorage(userLimits.maxDisk) }}</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedDisk, userLimits.maxDisk)"
                :color="getProgressColor(userLimits.usedDisk, userLimits.maxDisk)"
                :stroke-width="8"
              />
              <div class="limit-description">
                所有实例存储空间总量
              </div>
            </div>

            <!-- 流量限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">流量限制</span>
                <span class="limit-usage">
                  {{ userLimits.maxTraffic > 0 ? `${formatTraffic(userLimits.usedTraffic)} / ${formatTraffic(userLimits.maxTraffic)}` : '无限制' }}
                </span>
              </div>
              <el-progress 
                v-if="userLimits.maxTraffic > 0"
                :percentage="getUsagePercentage(userLimits.usedTraffic, userLimits.maxTraffic)"
                :color="getProgressColor(userLimits.usedTraffic, userLimits.maxTraffic)"
                :stroke-width="8"
              />
              <div v-else class="unlimited-badge">
                <el-tag type="success" size="small">无流量限制</el-tag>
              </div>
              <div class="limit-description">
                {{ userLimits.maxTraffic > 0 ? '当月流量配额使用情况' : '当前等级享有无限流量' }}
              </div>
            </div>
          </div>
        </el-card>
      </div>

      <!-- 流量使用统计 -->
      <TrafficOverview />

      <!-- 系统公告 -->
      <div
        v-if="announcements.length > 0"
        class="announcements"
      >
        <el-card>
          <template #header>
            <div class="card-header">
              <span>系统公告</span>
            </div>
          </template>
        
          <div class="announcements-list">
            <div 
              v-for="announcement in announcements" 
              :key="announcement.id"
              class="announcement-item"
            >
              <div class="announcement-title">
                {{ announcement.title }}
              </div>
              <div class="announcement-content">
                {{ announcement.content }}
              </div>
              <div class="announcement-date">
                {{ formatDate(announcement.createdAt) }}
              </div>
            </div>
          </div>
        </el-card>
      </div>
    </div> <!-- 结束主要内容区域 -->
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onActivated, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { 
  Refresh
} from '@element-plus/icons-vue'
import { getUserLimits } from '@/api/user'
import { getAnnouncements } from '@/api/public'
import { useUserStore } from '@/pinia/modules/user'
import { formatMemorySize, formatDiskSize, formatBandwidthSpeed } from '@/utils/unit-formatter'
import TrafficOverview from '@/components/TrafficOverview.vue'

const userStore = useUserStore()
const userInfo = userStore.user || {}
const loading = ref(true)

const userLimits = reactive({
  level: 1,
  maxInstances: 0,
  usedInstances: 0,
  maxCpu: 0,
  usedCpu: 0,
  maxMemory: 0,
  usedMemory: 0,
  maxDisk: 0,
  usedDisk: 0,
  maxBandwidth: 0,
  usedBandwidth: 0,
  maxTraffic: 0,
  usedTraffic: 0
})

const announcements = ref([])

// 获取用户限制信息
const loadUserLimits = async () => {
  const loadingMsg = ElMessage({
    message: '正在刷新配额信息...',
    type: 'info',
    duration: 0, // 不自动关闭
    showClose: false
  })
  
  try {
    const response = await getUserLimits()
    if (response.code === 0 || response.code === 200) {
      Object.assign(userLimits, response.data)
      loadingMsg.close()
      ElMessage.success('配额信息已刷新')
    } else {
      loadingMsg.close()
      ElMessage.error(response.message || '加载用户配额信息失败')
    }
  } catch (error) {
    console.error('获取用户限制失败:', error)
    loadingMsg.close()
    ElMessage.error('加载用户配额信息失败')
  }
}

// 获取公告信息
const loadAnnouncements = async () => {
  try {
    const response = await getAnnouncements({ page: 1, pageSize: 3 })
    if (response.code === 0 || response.code === 200) {
      announcements.value = response.data.list || []
    }
  } catch (error) {
    console.error('获取公告失败:', error)
  }
}

// 获取等级标签类型
const getLevelTagType = (level) => {
  const levelMap = {
    1: '',
    2: 'warning',
    3: 'success', 
    4: 'danger'
  }
  return levelMap[level] || ''
}

// 获取等级文本
const getLevelText = (level) => {
  const levelMap = {
    1: '普通用户',
    2: '高级用户',
    3: 'VIP用户',
    4: '超级用户'
  }
  return levelMap[level] || '未知等级'
}

// 获取等级描述
const getLevelDescription = (level) => {
  const descMap = {
    1: '享受吧',
    2: '享受吧',
    3: '享受吧',
    4: '享受吧'
  }
  return descMap[level] || '享受基础服务'
}

// 获取等级权益
const getLevelBenefits = (level) => {
  const benefitsMap = {
    1: [
      '最多创建 1 个实例',
    ],
    2: [
      '最多创建 2 个实例',
    ],
    3: [
      '最多创建 3 个实例',
    ],
    4: [
      '无限制实例创建',
    ]
  }
  return benefitsMap[level] || []
}

// 获取使用百分比
const getUsagePercentage = (used, total) => {
  if (!total) return 0
  return Math.round((used / total) * 100)
}

// 获取进度条颜色
const getProgressColor = (used, total) => {
  const percentage = getUsagePercentage(used, total)
  if (percentage >= 90) return '#f56c6c'
  if (percentage >= 70) return '#e6a23c'
  return '#67c23a'
}

// 格式化内存显示
const formatMemory = (memory) => {
  return formatMemorySize(memory)
}

// 格式化存储显示
const formatStorage = (disk) => {
  return formatDiskSize(disk)
}

// 格式化带宽显示
const formatBandwidth = (bandwidth) => {
  return formatBandwidthSpeed(bandwidth)
}

// 格式化流量显示
const formatTraffic = (traffic) => {
  return formatDiskSize(traffic) // 流量和磁盘都是以MB为单位，使用相同的格式化
}

// 格式化日期
const formatDate = (dateString) => {
  return new Date(dateString).toLocaleDateString('zh-CN')
}

onMounted(async () => {
  // 添加强制页面刷新监听器
  window.addEventListener('force-page-refresh', handleForceRefresh)
  
  loading.value = true
  try {
    await Promise.all([
      loadUserLimits(),
      loadAnnouncements()
    ])
  } finally {
    loading.value = false
  }
})

// 使用 onActivated 确保每次页面激活时都重新加载数据
onActivated(async () => {
  loading.value = true
  try {
    await Promise.all([
      loadUserLimits(),
      loadAnnouncements()
    ])
  } finally {
    loading.value = false
  }
})

// 处理强制刷新事件
const handleForceRefresh = async (event) => {
  if (event.detail && event.detail.path === '/user/dashboard') {
    loading.value = true
    try {
      await Promise.all([
        loadUserLimits(),
        loadAnnouncements()
      ])
    } finally {
      loading.value = false
    }
  }
}

onUnmounted(() => {
  // 清理事件监听器
  window.removeEventListener('force-page-refresh', handleForceRefresh)
})
</script>
<style scoped>
.user-dashboard {
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

.dashboard-header {
  margin-bottom: 24px;
}

.dashboard-header h1 {
  margin: 0 0 8px 0;
  color: #1f2937;
  font-size: 28px;
  font-weight: 600;
}

.dashboard-header p {
  margin: 0;
  color: #6b7280;
  font-size: 16px;
}

/* 用户等级信息 */
.user-level-section {
  margin-bottom: 24px;
}

.level-card {
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.15);
  border-left: 4px solid #10b981;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
  color: #1f2937;
}

.level-content {
  padding: 16px 0;
}

.level-info {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 24px;
}

.level-display {
  display: flex;
  align-items: center;
  gap: 16px;
}

.level-number {
  width: 60px;
  height: 60px;
  border-radius: 50%;
  background: linear-gradient(135deg, #10b981, #059669);
  color: white;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  font-weight: bold;
}

.level-description h3 {
  margin: 0 0 4px 0;
  font-size: 18px;
  color: #1f2937;
}

.level-description p {
  margin: 0;
  color: #6b7280;
  font-size: 14px;
}

.level-benefits h4 {
  margin: 0 0 12px 0;
  font-size: 16px;
  color: #1f2937;
}

.level-benefits ul {
  margin: 0;
  padding-left: 16px;
  color: #4b5563;
}

.level-benefits li {
  margin-bottom: 4px;
}

/* 资源限制信息 */
.resource-limits-section {
  margin-bottom: 24px;
}

.limits-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 20px;
}

.limit-item {
  padding: 16px;
  background: #f9fafb;
  border-radius: 8px;
  border: 1px solid #e5e7eb;
}

.limit-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.limit-title {
  font-weight: 600;
  color: #1f2937;
}

.limit-usage {
  font-weight: 600;
  color: #6b7280;
}

.limit-description {
  margin-top: 8px;
  font-size: 12px;
  color: #9ca3af;
}

/* 公告 */
.announcements {
  margin-bottom: 24px;
}

.announcements-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.announcement-item {
  padding: 16px;
  background: #f8fafc;
  border-radius: 8px;
  border-left: 4px solid #10b981;
}

.announcement-title {
  font-weight: 600;
  color: #1f2937;
  margin-bottom: 8px;
}

.announcement-content {
  color: #4b5563;
  margin-bottom: 8px;
  line-height: 1.5;
}

.announcement-date {
  font-size: 12px;
  color: #9ca3af;
}

.unlimited-badge {
  margin: 8px 0;
  text-align: center;
}

/* 响应式设计 */
@media (max-width: 768px) {
  .level-info {
    grid-template-columns: 1fr;
  }
  
  .limits-grid {
    grid-template-columns: 1fr;
  }
}
</style>
<template>
  <div class="user-dashboard">
    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-container"
    >
      <el-loading-directive />
      <div class="loading-text">
        {{ t('common.loading') }}
      </div>
    </div>
    
    <!-- 主要内容 -->
    <div v-else>
      <div class="dashboard-header">
        <h1>
          {{ t('user.dashboard.welcome', { name: userInfo?.nickname || userInfo?.username || t('common.user') }) }}
          <el-tag
            :type="getLevelTagType(userLimits.level)"
            size="large"
            effect="plain"
            class="level-tag"
          >
            {{ t('user.dashboard.levelTag', { level: userLimits.level, text: getLevelText(userLimits.level) }) }}
          </el-tag>
        </h1>
        <p>{{ t('user.dashboard.subtitle') }}</p>
      </div>

      <!-- 资源限制信息 -->
      <div class="resource-limits-section">
        <el-card>
          <template #header>
            <div class="card-header">
              <span>{{ t('user.dashboard.resourceQuota') }}</span>
              <el-button
                size="small"
                @click="loadUserLimits"
              >
                <el-icon><Refresh /></el-icon>
                {{ t('common.refresh') }}
              </el-button>
            </div>
          </template>
        
          <div class="limits-grid">
            <!-- 实例数量限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">{{ t('user.dashboard.instanceCount') }}</span>
                <span class="limit-usage">{{ userLimits.usedInstances }} / {{ userLimits.maxInstances }}</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedInstances, userLimits.maxInstances)"
                :color="getProgressColor(userLimits.usedInstances, userLimits.maxInstances)"
                :stroke-width="8"
              />
              <div class="limit-description">
                {{ t('user.dashboard.instanceCountDesc') }}
                <span v-if="userLimits.containerCount !== undefined || userLimits.vmCount !== undefined" style="display: block; margin-top: 4px; color: #909399; font-size: 12px;">
                  {{ t('user.dashboard.containerCount') }}: {{ userLimits.containerCount || 0 }} / {{ t('user.dashboard.vmCount') }}: {{ userLimits.vmCount || 0 }}
                </span>
              </div>
            </div>

            <!-- CPU核心限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">{{ t('user.dashboard.cpuCores') }}</span>
                <span class="limit-usage">{{ userLimits.usedCpu }} / {{ userLimits.maxCpu }}{{ t('user.dashboard.cores') }}</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedCpu, userLimits.maxCpu)"
                :color="getProgressColor(userLimits.usedCpu, userLimits.maxCpu)"
                :stroke-width="8"
              />
              <div class="limit-description">
                {{ t('user.dashboard.cpuCoresDesc') }}
              </div>
            </div>

            <!-- 内存限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">{{ t('user.dashboard.memorySize') }}</span>
                <span class="limit-usage">{{ formatMemory(userLimits.usedMemory) }} / {{ formatMemory(userLimits.maxMemory) }}</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedMemory, userLimits.maxMemory)"
                :color="getProgressColor(userLimits.usedMemory, userLimits.maxMemory)"
                :stroke-width="8"
              />
              <div class="limit-description">
                {{ t('user.dashboard.memorySizeDesc') }}
              </div>
            </div>

            <!-- 存储空间限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">{{ t('user.dashboard.storageSpace') }}</span>
                <span class="limit-usage">{{ formatStorage(userLimits.usedDisk) }} / {{ formatStorage(userLimits.maxDisk) }}</span>
              </div>
              <el-progress 
                :percentage="getUsagePercentage(userLimits.usedDisk, userLimits.maxDisk)"
                :color="getProgressColor(userLimits.usedDisk, userLimits.maxDisk)"
                :stroke-width="8"
              />
              <div class="limit-description">
                {{ t('user.dashboard.storageSpaceDesc') }}
              </div>
            </div>

            <!-- 流量限制 -->
            <div class="limit-item">
              <div class="limit-header">
                <span class="limit-title">{{ t('user.dashboard.trafficLimit') }}</span>
                <span class="limit-usage">
                  {{ userLimits.maxTraffic > 0 ? `${formatTraffic(userLimits.usedTraffic)} / ${formatTraffic(userLimits.maxTraffic)}` : t('user.dashboard.unlimited') }}
                </span>
              </div>
              <el-progress 
                v-if="userLimits.maxTraffic > 0"
                :percentage="getUsagePercentage(userLimits.usedTraffic, userLimits.maxTraffic)"
                :color="getProgressColor(userLimits.usedTraffic, userLimits.maxTraffic)"
                :stroke-width="8"
              />
              <div
                v-else
                class="unlimited-badge"
              >
                <el-tag
                  type="success"
                  size="small"
                >
                  {{ t('user.dashboard.unlimitedTraffic') }}
                </el-tag>
              </div>
              <div class="limit-description">
                {{ userLimits.maxTraffic > 0 ? t('user.dashboard.trafficLimitDesc') : t('user.dashboard.unlimitedTrafficDesc') }}
              </div>
            </div>
          </div>
        </el-card>
      </div>

      <!-- 流量使用统计 -->
      <TrafficOverview />

      <!-- 流量历史趋势图 -->
      <div class="traffic-history-section">
        <TrafficHistoryChart
          type="user"
          :title="t('user.dashboard.trafficHistoryChart')"
          :auto-refresh="0"
        />
      </div>

      <!-- 系统公告 -->
      <div
        v-if="announcements.length > 0"
        class="announcements"
      >
        <el-card>
          <template #header>
            <div class="card-header">
              <span>{{ t('user.dashboard.systemAnnouncements') }}</span>
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
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { 
  Refresh
} from '@element-plus/icons-vue'
import { getUserLimits } from '@/api/user'
import { getAnnouncements } from '@/api/public'
import { useUserStore } from '@/pinia/modules/user'
import { formatMemorySize, formatDiskSize, formatBandwidthSpeed } from '@/utils/unit-formatter'
import TrafficOverview from '@/components/TrafficOverview.vue'
import TrafficHistoryChart from '@/components/TrafficHistoryChart.vue'

const { t, locale } = useI18n()
const userStore = useUserStore()
const userInfo = userStore.user || {}
const loading = ref(true)

const userLimits = reactive({
  level: 1,
  maxInstances: 0,
  usedInstances: 0,
  containerCount: 0,
  vmCount: 0,
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
    message: t('user.dashboard.refreshingQuota'),
    type: 'info',
    duration: 0, // 不自动关闭
    showClose: false
  })
  
  try {
    const response = await getUserLimits()
    if (response.code === 0 || response.code === 200) {
      Object.assign(userLimits, response.data)
      loadingMsg.close()
      ElMessage.success(t('user.dashboard.quotaRefreshed'))
    } else {
      loadingMsg.close()
      ElMessage.error(response.message || t('user.dashboard.loadQuotaFailed'))
    }
  } catch (error) {
    console.error(t('user.dashboard.getUserLimitsFailed'), error)
    loadingMsg.close()
    ElMessage.error(t('user.dashboard.loadQuotaFailed'))
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
    console.error(t('user.dashboard.getAnnouncementsFailed'), error)
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
const getLevelText = () => {
  return t('user.dashboard.normalUser')
}

// 获取等级描述
const getLevelDescription = () => {
  return t('user.dashboard.enjoyIt')
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
  return new Date(dateString).toLocaleDateString(locale.value === 'en-US' ? 'en-US' : 'zh-CN')
}

onMounted(async () => {
  // 强制页面刷新监听器
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
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.dashboard-header p {
  margin: 0;
  color: #6b7280;
  font-size: 16px;
}

.level-tag {
  font-size: 14px;
  font-weight: 500;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
  color: #1f2937;
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

/* 流量历史图表 */
.traffic-history-section {
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
  .dashboard-header h1 {
    font-size: 24px;
  }
  
  .limits-grid {
    grid-template-columns: 1fr;
  }
}
</style>
<template>
  <div class="instance-detail">
    <!-- 页面头部 -->
    <div class="page-header">
      <el-button 
        type="text" 
        class="back-btn"
        @click="handleBack"
      >
        <el-icon><ArrowLeft /></el-icon>
        {{ t('user.instanceDetail.backToList') }}
      </el-button>
    </div>

    <!-- 实例概览卡片 -->
    <InstanceOverviewCard
      :instance="instance"
      :monitoring="monitoring"
      :action-loading="actionLoading"
      :instance-type-permissions="instanceTypePermissions"
      :share-mode="isShareMode"
      @perform-action="performAction"
      @reset-password="showResetPasswordDialog"
      @open-ssh="openSSHTerminal"
      @view-task="viewTaskDetail"
      @create-share="createShareLink"
    />

    <!-- 标签页内容 -->
    <el-card class="tabs-card">
      <el-tabs
        v-model="activeTab"
        type="border-card"
      >
        <!-- 概览标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.overview')"
          name="overview"
        >
          <OverviewTab
            :instance="instance"
            :show-password="showPassword"
            @toggle-password="togglePassword"
            @copy="copyToClipboard"
          />
        </el-tab-pane>

        <!-- 端口映射标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.portMapping')"
          name="ports"
        >
          <PortMappingsTab
            :instance="instance"
            :port-mappings="portMappings"
            @refresh="refreshPortMappings"
            @copy="copyToClipboard"
          />
        </el-tab-pane>

        <!-- 统计标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.statistics')"
          name="stats"
        >
          <StatisticsTab
            :instance="instance"
            :monitoring="monitoring"
            :instance-id="currentInstanceId"
            :share-token="shareToken"
            @refresh="refreshMonitoring"
            @show-traffic-detail="showTrafficDetail = true"
          />
        </el-tab-pane>
        <!-- 资源监控标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.resourceMonitoring')"
          name="resources"
        >
          <ResourceMonitorChart
            ref="resourceChartRef"
            :instance-id="currentInstanceId"
            :share-token="shareToken"
            :auto-refresh="300000"
          />
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- PMAcct 流量详情对话框 -->
    <InstanceTrafficDetail
      v-model="showTrafficDetail"
      :instance-id="currentInstanceId"
      :share-token="shareToken"
      :instance-name="instance.name"
    />

    <!-- 重置系统镜像选择对话框 -->
    <el-dialog
      v-model="showResetImageDialog"
      :title="t('user.instanceDetail.selectResetImage')"
      width="500px"
      destroy-on-close
    >
      <div v-loading="loadingResetImages">
        <p style="margin-bottom: 12px; color: var(--el-text-color-secondary);">
          {{ t('user.instanceDetail.selectResetImageTip') }}
        </p>
        <el-radio-group
          v-model="selectedResetImage"
          style="display: flex; flex-direction: column; gap: 8px;"
        >
          <el-radio
            v-for="img in resetImages"
            :key="img.name || img.id"
            :value="img.name"
            border
            style="margin: 0; width: 100%;"
          >
            <span style="display: inline-flex; align-items: center; gap: 6px;">
              <OsIcon
                :name="img.name"
                :size="20"
              />
              {{ img.display_name || img.name }}
            </span>
          </el-radio>
        </el-radio-group>
      </div>
      <template #footer>
        <el-button @click="showResetImageDialog = false">
          {{ t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :disabled="!selectedResetImage"
          @click="confirmResetWithImage"
        >
          {{ t('user.instanceDetail.confirmReset') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import InstanceTrafficDetail from '@/components/InstanceTrafficDetail.vue'
import ResourceMonitorChart from '@/components/ResourceMonitorChart.vue'
import OsIcon from '@/components/OsIcon.vue'
import { useInstanceDetail } from './composables/useInstanceDetail'
import { useInstanceActions } from './composables/useInstanceActions'
import InstanceOverviewCard from './components/InstanceOverviewCard.vue'
import OverviewTab from './components/OverviewTab.vue'
import PortMappingsTab from './components/PortMappingsTab.vue'
import StatisticsTab from './components/StatisticsTab.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const activeTab = ref('overview')
const resourceChartRef = ref(null)
const shareToken = computed(() => route.params.token ? String(route.params.token) : '')
const isShareMode = computed(() => Boolean(shareToken.value))

const {
  portMappings,
  instanceTypePermissions,
  instance,
  monitoring,
  updateInstancePermissions,
  loadInstanceDetail,
  refreshPortMappings,
  refreshMonitoring,
  loadInstanceTypePermissions,
  startShareTokenMonitor
} = useInstanceDetail(shareToken)
const currentInstanceId = computed(() => instance.value?.id || route.params.id || '')

const {
  actionLoading,
  showPassword,
  showTrafficDetail,
  showResetImageDialog,
  resetImages,
  selectedResetImage,
  loadingResetImages,
  confirmResetWithImage,
  viewTaskDetail,
  performAction,
  openSSHTerminal,
  createShareLink,
  showResetPasswordDialog,
  togglePassword,
  copyToClipboard
} = useInstanceActions(instance, monitoring, loadInstanceDetail, shareToken)

// 标志位，防止 watch 循环触发
let isUpdatingFromRoute = false

const handleBack = () => {
  if (isShareMode.value) {
    router.push('/home')
    return
  }
  router.back()
}

watch(() => [route.params.id, route.params.token], async ([newId, newToken], [oldId, oldToken] = []) => {
  if ((newToken && newToken !== oldToken) || (newId && newId !== oldId && newId !== 'undefined')) {
    try {
      const [detailSuccess, permissionsSuccess] = await Promise.all([
        loadInstanceDetail(true),
        loadInstanceTypePermissions()
      ])
      if (detailSuccess && permissionsSuccess) {
        updateInstancePermissions()
        refreshMonitoring()
        refreshPortMappings()
      }
    } catch (error) {
      console.error('路由切换时加载数据失败:', error)
    }
  }
})

watch(() => route.query.tab, (newTab, oldTab) => {
  if (newTab === oldTab) return
  if (newTab && ['overview', 'ports', 'stats', 'resources'].includes(newTab)) {
    if (activeTab.value === newTab) return
    isUpdatingFromRoute = true
    activeTab.value = newTab
    nextTick(() => { isUpdatingFromRoute = false })
  } else {
    if (activeTab.value !== 'overview') {
      isUpdatingFromRoute = true
      activeTab.value = 'overview'
      nextTick(() => { isUpdatingFromRoute = false })
    }
  }
}, { immediate: true })

watch(activeTab, (newTab, oldTab) => {
  if (newTab === oldTab || isUpdatingFromRoute) return
  if (newTab && route.query.tab !== newTab) {
    router.replace({ query: { ...route.query, tab: newTab } })
  }
})

let monitoringTimer = null

onMounted(async () => {
  await nextTick()
  try {
    const [detailSuccess, permissionsSuccess] = await Promise.all([
      loadInstanceDetail(true),
      loadInstanceTypePermissions()
    ])
    if (detailSuccess && permissionsSuccess) {
      updateInstancePermissions()
      refreshMonitoring()
      refreshPortMappings()
      startShareTokenMonitor() // 定时检测分享令牌有效期
      monitoringTimer = setInterval(refreshMonitoring, 30000)
    }
  } catch (error) {
    console.error('页面初始化失败:', error)
  }
})

onUnmounted(() => {
  if (monitoringTimer) {
    clearInterval(monitoringTimer)
    monitoringTimer = null
  }
})
</script>

<style src="./detail.css" scoped></style>

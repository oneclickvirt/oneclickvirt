<template>
  <div class="user-apply">
    <div class="page-header">
      <h1>{{ t('user.apply.title') }}</h1>
      <p>{{ t('user.apply.subtitle') }}</p>
    </div>

    <!-- 用户等级和限制信息 -->
    <el-card class="user-limits-card">
      <template #header>
        <div class="card-header">
          <span>{{ t('user.apply.userQuotaInfo') }}</span>
        </div>
      </template>
      <div class="limits-grid">
        <div class="limit-item">
          <span class="label">{{ t('user.apply.maxInstances') }}</span>
          <span class="value">
            {{ userLimits.usedInstances }} / {{ userLimits.maxInstances }}
            <span v-if="userLimits.containerCount !== undefined || userLimits.vmCount !== undefined" style="color: #909399; font-size: 12px; margin-left: 8px;">
              ({{ t('user.dashboard.containerCount') }}: {{ userLimits.containerCount || 0 }} / {{ t('user.dashboard.vmCount') }}: {{ userLimits.vmCount || 0 }})
            </span>
          </span>
        </div>
        <div class="limit-item">
          <span class="label">{{ t('user.apply.cpuCoreLimit') }}</span>
          <span class="value">{{ userLimits.usedCpu }} / {{ userLimits.maxCpu }}{{ t('user.apply.cores') }}</span>
        </div>
        <div class="limit-item">
          <span class="label">{{ t('user.apply.memoryLimit') }}</span>
          <span class="value">{{ formatResourceUsage(userLimits.usedMemory, userLimits.maxMemory, 'memory') }}</span>
        </div>
        <div class="limit-item">
          <span class="label">{{ t('user.apply.diskLimit') }}</span>
          <span class="value">{{ formatResourceUsage(userLimits.usedDisk, userLimits.maxDisk, 'disk') }}</span>
        </div>
        <div class="limit-item">
          <span class="label">{{ t('user.apply.trafficLimit') }}</span>
          <span class="value">{{ formatResourceUsage(userLimits.usedTraffic, userLimits.maxTraffic, 'disk') }}</span>
        </div>
      </div>
    </el-card>

    <!-- 服务器选择 -->
    <el-card class="providers-card">
      <template #header>
        <div class="card-header">
          <span>{{ t('user.apply.selectProvider') }}</span>
        </div>
      </template>
      <div class="providers-grid">
        <div 
          v-for="provider in providers" 
          :key="provider.id"
          class="provider-card"
          :class="{ 
            'selected': selectedProvider?.id === provider.id,
            'active': provider.status === 'active',
            'offline': provider.status === 'offline' || provider.status === 'inactive',
            'partial': provider.status === 'partial'
          }"
          @click="selectProvider(provider)"
        >
          <div class="provider-header">
            <h3>{{ provider.name }}</h3>
            <el-tag 
              :type="getProviderStatusType(provider.status)"
              size="small"
            >
              {{ getProviderStatusText(provider.status) }}
            </el-tag>
          </div>
          <div class="provider-info">
            <div class="info-item">
              <span class="location-info">
                <span
                  v-if="provider.countryCode"
                  class="flag-icon"
                >{{ getFlagEmoji(provider.countryCode) }}</span>
                {{ t('user.apply.location') }}: {{ formatProviderLocation(provider) }}
              </span>
            </div>
            <div class="info-item">
              <span>CPU: {{ provider.cpu }}{{ t('user.apply.cores') }}</span>
            </div>
            <div class="info-item">
              <span>{{ t('user.apply.memoryLimit') }}: {{ formatMemorySize(provider.memory || 0) }}</span>
            </div>
            <div class="info-item">
              <span>{{ t('user.apply.diskLimit') }}: {{ formatDiskSize(provider.disk || 0) }}</span>
            </div>
            <div 
              v-if="provider.containerEnabled && provider.vmEnabled"
              class="info-item"
            >
              <span>
                {{ t('user.apply.availableInstances') }}: 
                {{ t('user.apply.container') }}{{ provider.availableContainerSlots === -1 ? t('user.apply.unlimited') : provider.availableContainerSlots }} / 
                {{ t('user.apply.vm') }}{{ provider.availableVMSlots === -1 ? t('user.apply.unlimited') : provider.availableVMSlots }}
              </span>
            </div>
            <div 
              v-else-if="provider.containerEnabled"
              class="info-item"
            >
              <span>{{ t('user.apply.availableInstances') }}: {{ provider.availableContainerSlots === -1 ? t('user.apply.unlimited') : provider.availableContainerSlots }}</span>
            </div>
            <div 
              v-else-if="provider.vmEnabled"
              class="info-item"
            >
              <span>{{ t('user.apply.availableInstances') }}: {{ provider.availableVMSlots === -1 ? t('user.apply.unlimited') : provider.availableVMSlots }}</span>
            </div>
          </div>
        </div>
      </div>
    </el-card>

    <!-- 配置表单 -->
    <el-card
      v-if="selectedProvider"
      class="config-card"
    >
      <template #header>
        <div class="card-header">
          <span>{{ t('user.apply.configInstance') }} - {{ selectedProvider.name }}</span>
        </div>
      </template>
      <el-form 
        v-if="!selectedProvider.redeemCodeOnly"
        ref="formRef"
        :model="configForm"
        :rules="configRules"
        label-width="120px"
      >
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="t('user.apply.instanceType')"
              prop="type"
            >
              <el-select
                v-model="configForm.type"
                :placeholder="t('user.apply.selectInstanceType')"
                @change="onInstanceTypeChange"
              >
                <el-option 
                  :label="t('user.apply.container')" 
                  value="container" 
                  :disabled="!canCreateInstanceType('container')"
                />
                <el-option 
                  :label="t('user.apply.vm')" 
                  value="vm" 
                  :disabled="!canCreateInstanceType('vm')"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="t('user.apply.systemImage')"
              prop="imageId"
            >
              <el-select
                v-model="configForm.imageId"
                :placeholder="t('user.apply.selectSystemImage')"
              >
                <el-option 
                  v-for="image in availableImages" 
                  :key="image.id" 
                  :label="image.name" 
                  :value="image.id"
                >
                  <span>{{ image.name }}</span>
                  <span style="float: right; color: #8492a6; font-size: 12px; margin-left: 10px">
                    {{ formatImageRequirements(image) }}
                  </span>
                </el-option>
              </el-select>
              <div
                v-if="selectedImageInfo"
                class="form-hint"
                style="margin-top: 5px; font-size: 12px; color: #909399;"
              >
                {{ t('user.apply.imageRequirements', { 
                  memory: selectedImageInfo.minMemoryMB, 
                  disk: Math.round(selectedImageInfo.minDiskMB / 1024 * 10) / 10 
                }) }}
              </div>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="t('user.apply.cpuSpec')"
              prop="cpuId"
            >
              <el-select
                v-model="configForm.cpuId"
                :placeholder="t('user.apply.selectCpuSpec')"
              >
                <el-option 
                  v-for="cpu in availableCpuSpecs" 
                  :key="cpu.id" 
                  :label="cpu.name" 
                  :value="cpu.id"
                  :disabled="!canSelectSpec('cpu', cpu)"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="t('user.apply.memorySpec')"
              prop="memoryId"
            >
              <el-select
                v-model="configForm.memoryId"
                :placeholder="t('user.apply.selectMemorySpec')"
              >
                <el-option 
                  v-for="memory in availableMemorySpecs" 
                  :key="memory.id" 
                  :label="memory.name" 
                  :value="memory.id"
                  :disabled="!canSelectSpec('memory', memory)"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="t('user.apply.diskSpec')"
              prop="diskId"
            >
              <el-select
                v-model="configForm.diskId"
                :placeholder="t('user.apply.selectDiskSpec')"
              >
                <el-option 
                  v-for="disk in availableDiskSpecs" 
                  :key="disk.id" 
                  :label="disk.name" 
                  :value="disk.id"
                  :disabled="!canSelectSpec('disk', disk)"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="t('user.apply.bandwidthSpec')"
              prop="bandwidthId"
            >
              <el-select
                v-model="configForm.bandwidthId"
                :placeholder="t('user.apply.selectBandwidthSpec')"
              >
                <el-option 
                  v-for="bandwidth in availableBandwidthSpecs" 
                  :key="bandwidth.id" 
                  :label="bandwidth.name" 
                  :value="bandwidth.id"
                  :disabled="!canSelectSpec('bandwidth', bandwidth)"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item :label="t('user.apply.remarks')">
          <el-input 
            v-model="configForm.description"
            type="textarea"
            :rows="3"
            :placeholder="t('user.apply.remarksPlaceholder')"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>

        <el-form-item>
          <el-button 
            type="primary" 
            :loading="submitting"
            size="large"
            @click="submitApplication"
          >
            {{ t('user.apply.submitApplication') }}
          </el-button>
          <el-button
            size="large"
            @click="resetForm"
          >
            {{ t('user.apply.resetConfig') }}
          </el-button>
        </el-form-item>
      </el-form>

      <!-- 兑换码兑换表单 -->
      <div v-else class="redeem-card">
        <el-form label-width="120px">
          <el-form-item :label="t('user.apply.redeemCodeTitle')">
            <el-input
              v-model="redeemCodeInput"
              :placeholder="t('user.apply.redeemCodePlaceholder')"
              style="max-width: 340px"
              @keyup.enter="submitRedemption"
            />
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              :loading="redeemSubmitting"
              size="large"
              @click="submitRedemption"
            >
              {{ t('user.apply.redeemCodeSubmit') }}
            </el-button>
          </el-form-item>
        </el-form>
      </div>
    </el-card>

    <!-- 空状态 -->
    <el-empty 
      v-if="providers.length === 0 && !loading"
      :description="t('user.apply.noProvidersDescription')"
    >
      <template #description>
        <p>{{ t('user.apply.noProvidersMessage') }}</p>
        <p style="font-size: 12px; color: #909399; margin-top: 8px;">
          {{ t('user.apply.noProvidersHint') }}
        </p>
      </template>
      <el-button
        type="primary"
        @click="() => loadProviders(true)"
      >
        {{ t('user.apply.refresh') }}
      </el-button>
    </el-empty>

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
  </div>
</template>

<script setup>
import { onMounted, watch, onActivated, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { formatMemorySize, formatDiskSize, formatResourceUsage } from '@/utils/unit-formatter'
import { getFlagEmoji } from '@/utils/countries'
import { useI18n } from 'vue-i18n'
import { useApplyProviders } from './composables/useApplyProviders'
import { useApplyForm } from './composables/useApplyForm'

const route = useRoute()
const { t, locale } = useI18n()

const {
  loading, refreshing, providers, selectedProvider, providerCapabilities,
  instanceTypePermissions,
  getProviderStatusType, getProviderStatusText, formatProviderLocation,
  canCreateInstanceType, loadProviders,
  loadProviderCapabilities, loadInstanceTypePermissions
} = useApplyProviders()

const {
  submitting, redeemCodeInput, redeemSubmitting,
  availableImages, instanceConfig, userLimits, configForm, formRef,
  configRules, availableCpuSpecs, selectedImageInfo,
  availableMemorySpecs, availableDiskSpecs, availableBandwidthSpecs,
  canSelectSpec, formatCpuSpecName, formatImageRequirements,
  loadUserLimits, loadInstanceConfig, loadFilteredImages,
  autoSelectFirstAvailableSpecs, onInstanceTypeChange,
  submitApplication, submitRedemption, resetForm
} = useApplyForm(selectedProvider, providerCapabilities, loadProviderCapabilities, canCreateInstanceType)

// Bridge: refresh providers + userLimits together
const refreshData = async () => {
  if (refreshing.value || loading.value) return
  try {
    refreshing.value = true
    await Promise.allSettled([loadProviders(), loadUserLimits()])
    ElMessage.success(t('user.apply.dataRefreshed'))
  } catch (error) {
    console.error('refreshData failed:', error)
    ElMessage.error(t('user.apply.refreshFailed'))
  } finally {
    refreshing.value = false
  }
}

// Bridge: select provider (uses both provider and form state)
const selectProvider = async (provider) => {
  if (provider.status === 'offline' || provider.status === 'inactive') {
    ElMessage.warning('该节点当前离线，无法选择')
    return
  }
  const hasAvailableContainer = provider.containerEnabled && (provider.availableContainerSlots === -1 || provider.availableContainerSlots > 0)
  const hasAvailableVM = provider.vmEnabled && (provider.availableVMSlots === -1 || provider.availableVMSlots > 0)
  if (!hasAvailableContainer && !hasAvailableVM) {
    ElMessage.warning('该节点资源不足，无法创建新实例')
    return
  }
  selectedProvider.value = provider
  await loadProviderCapabilities(provider.id)
  await loadInstanceConfig(provider.id)
  if (!canCreateInstanceType(configForm.type)) {
    const capabilities = providerCapabilities.value[provider.id]
    if (capabilities?.supportedTypes?.length > 0) {
      for (const type of ['container', 'vm']) {
        if (capabilities.supportedTypes.includes(type) && canCreateInstanceType(type)) {
          configForm.type = type
          break
        }
      }
    }
  }
  if (configForm.type) await loadFilteredImages()
  configForm.imageId = ''
  autoSelectFirstAvailableSpecs()
}

// 监听路由变化
watch(() => route.path, (newPath, oldPath) => {
  if (newPath === '/user/apply' && oldPath !== newPath) {
    loadProviders()
    loadUserLimits()
    loadInstanceConfig()
  }
}, { immediate: false })

// 监听镜像选择变化
watch(() => configForm.imageId, (newImageId, oldImageId) => {
  if (newImageId !== oldImageId && newImageId) {
    const selectedImage = availableImages.value.find(img => img.id === newImageId)
    if (selectedImage && selectedImage.minMemoryMB && selectedImage.minDiskMB) {
      const minMemoryMB = selectedImage.minMemoryMB
      const minDiskMB = selectedImage.minDiskMB
      let needAutoSelect = false
      if (configForm.memoryId) {
        const currentMemory = instanceConfig.value.memorySpecs?.find(spec => spec.id === configForm.memoryId)
        if (currentMemory && currentMemory.sizeMB < minMemoryMB) {
          configForm.memoryId = ''
          needAutoSelect = true
          ElMessage.warning('镜像类型变更，当前内存规格不符合最低要求，已自动选择合适的规格')
        }
      }
      if (configForm.diskId) {
        const currentDisk = instanceConfig.value.diskSpecs?.find(spec => spec.id === configForm.diskId)
        if (currentDisk && currentDisk.sizeMB < minDiskMB) {
          configForm.diskId = ''
          needAutoSelect = true
          ElMessage.warning('镜像类型变更，当前磁盘规格不符合最低要求，已自动选择合适的规格')
        }
      }
      if (needAutoSelect) autoSelectFirstAvailableSpecs()
    }
  }
})

// 监听 Provider 选择变化，重新验证规格
watch(() => selectedProvider.value?.type, (newProviderType, oldProviderType) => {
  if (newProviderType !== oldProviderType && configForm.imageId) {
    const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
    if (selectedImage && selectedImage.minDiskMB) {
      const currentDisk = instanceConfig.value.diskSpecs?.find(spec => spec.id === configForm.diskId)
      if (currentDisk && currentDisk.sizeMB < selectedImage.minDiskMB) {
        configForm.diskId = ''
        ElMessage.warning('Provider变更，当前磁盘规格不符合镜像的最低要求，已自动重置')
        if (availableDiskSpecs.value.length > 0) configForm.diskId = availableDiskSpecs.value[0].id
      }
    }
  }
})

const handleRouterNavigation = (event) => {
  if (event.detail && event.detail.path === '/user/apply') {
    loadProviders()
    loadUserLimits()
    loadInstanceTypePermissions()
    loadInstanceConfig()
  }
}

const handleForceRefresh = async (event) => {
  if (event.detail && event.detail.path === '/user/apply') {
    try {
      await loadInstanceTypePermissions()
      await loadProviders()
      Promise.allSettled([loadInstanceConfig(), loadUserLimits()])
    } catch (error) {
      console.error('强制刷新时数据加载失败:', error)
    }
  }
}

onMounted(async () => {
  window.addEventListener('router-navigation', handleRouterNavigation)
  window.addEventListener('force-page-refresh', handleForceRefresh)
  try {
    await loadInstanceTypePermissions()
    await loadProviders()
    Promise.allSettled([loadInstanceConfig(), loadUserLimits()])
  } catch (error) {
    console.error('页面初始化失败:', error)
    ElMessage.error('页面加载失败，请稍后重试')
  }
})

onActivated(async () => {
  try {
    await loadInstanceTypePermissions()
    await loadProviders()
    Promise.allSettled([loadInstanceConfig(), loadUserLimits()])
  } catch (error) {
    console.error('页面激活时数据加载失败:', error)
  }
})

onUnmounted(() => {
  window.removeEventListener('router-navigation', handleRouterNavigation)
  window.removeEventListener('force-page-refresh', handleForceRefresh)
})
</script>

<style scoped>
.user-apply {
  padding: 24px;
}

.page-header {
  margin-bottom: 24px;
}

.page-header h1 {
  margin: 0 0 8px 0;
  font-size: 24px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.page-header p {
  margin: 0;
  color: var(--text-color-secondary);
}

.user-limits-card,
.providers-card,
.config-card {
  margin-bottom: 24px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.redeem-card {
  padding: 8px 0;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
  color: var(--text-color-primary);
}

.limits-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
}

.limit-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px;
  background: var(--neutral-bg);
  border-radius: 8px;
}

.limit-item .label {
  color: var(--text-color-secondary);
  font-weight: 500;
}

.limit-item .value {
  color: var(--text-color-primary);
  font-weight: 600;
}

.providers-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 16px;
}

.provider-card {
  border: 2px solid var(--border-color);
  border-radius: 12px;
  padding: 16px;
  cursor: pointer;
  transition: all 0.3s ease;
  background-color: var(--card-bg-solid);
}

.provider-card:hover {
  border-color: #3b82f6;
  box-shadow: 0 4px 12px rgba(59, 130, 246, 0.15);
  transform: translateY(-2px);
}

.provider-card.selected {
  border-color: #3b82f6;
  background-color: var(--info-bg);
  box-shadow: 0 4px 16px rgba(59, 130, 246, 0.2);
}

/* Active状态 - 绿色 */
.provider-card.active {
  border-color: #10b981;
  background-color: var(--success-bg);
}

.provider-card.active:hover {
  border-color: #059669;
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.2);
}

.provider-card.active.selected {
  border-color: #059669;
  background-color: var(--success-bg);
  box-shadow: 0 4px 16px rgba(16, 185, 129, 0.25);
}

/* Offline状态 - 红色 */
.provider-card.offline {
  border-color: #ef4444;
  background-color: var(--error-bg);
  cursor: not-allowed;
  opacity: 0.7;
  position: relative;
}

.provider-card.offline::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(239, 68, 68, 0.1);
  border-radius: 10px;
  pointer-events: none;
}

.provider-card.offline:hover {
  border-color: #dc2626;
  box-shadow: 0 4px 12px rgba(239, 68, 68, 0.2);
  transform: none;
}

.provider-card.offline * {
  color: #9ca3af !important;
}

/* Partial状态 - 黄色 */
.provider-card.partial {
  border-color: #f59e0b;
  background-color: var(--warning-bg);
}

.provider-card.partial:hover {
  border-color: #d97706;
  box-shadow: 0 4px 12px rgba(245, 158, 11, 0.2);
}

.provider-card.partial.selected {
  border-color: #d97706;
  background-color: var(--warning-bg);
  box-shadow: 0 4px 16px rgba(245, 158, 11, 0.25);
}

.provider-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.provider-header h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.provider-info {
  margin-bottom: 12px;
}

.location-info {
  display: flex;
  align-items: center;
  gap: 6px;
}

.country-flag {
  width: 16px;
  height: 12px;
  border-radius: 2px;
  flex-shrink: 0;
}

.info-item {
  margin-bottom: 4px;
  font-size: 14px;
  color: var(--text-color-secondary);
}

.loading-container {
  padding: 24px;
}
</style>

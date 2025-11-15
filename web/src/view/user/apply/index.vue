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
          <span class="value">{{ userLimits.usedInstances }} / {{ userLimits.maxInstances }}</span>
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
          <el-button
            size="small"
            @click="() => loadProviders(true)"
          >
            <el-icon><Refresh /></el-icon>
            {{ t('user.apply.refresh') }}
          </el-button>
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
            重置配置
          </el-button>
        </el-form-item>
      </el-form>
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
import { ref, reactive, computed, onMounted, watch, onActivated, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { 
  getAvailableProviders, 
  getUserLimits,
  getFilteredImages,
  getProviderCapabilities,
  getUserInstanceTypePermissions,
  getInstanceConfig,
  createInstance
} from '@/api/user'
import { formatMemorySize, formatDiskSize, formatResourceUsage } from '@/utils/unit-formatter'
import { getFlagEmoji } from '@/utils/countries'

const { t, locale } = useI18n()
const router = useRouter()
const route = useRoute()

const loading = ref(false)
const submitting = ref(false)
const selectedProvider = ref(null)
const providers = ref([])
const availableImages = ref([])
const providerCapabilities = ref({})
const instanceTypePermissions = ref({
  canCreateContainer: false,
  canCreateVM: false,
  availableTypes: [],
  quotaInfo: {
    usedInstances: 0,
    maxInstances: 0,
    usedCpu: 0,
    maxCpu: 0,
    usedMemory: 0,
    maxMemory: 0
  }
})

// 规格配置数据
const instanceConfig = ref({
  cpuSpecs: [],
  memorySpecs: [],
  diskSpecs: [],
  bandwidthSpecs: []
})

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

const configForm = reactive({
  type: 'container', // 默认设置为容器类型，因为所有等级都可以创建
  imageId: '',
  cpuId: '', // 使用规格ID而不是数值
  memoryId: '', // 使用规格ID而不是数值
  diskId: '', // 使用规格ID而不是数值
  bandwidthId: '', // 使用规格ID而不是数值
  description: ''
})

const configRules = computed(() => ({
  type: [
    { required: true, message: t('user.apply.pleaseSelectInstanceType'), trigger: 'change' }
  ],
  imageId: [
    { required: true, message: t('user.apply.pleaseSelectSystemImage'), trigger: 'change' }
  ],
  cpuId: [
    { required: true, message: t('user.apply.pleaseSelectCpuSpec'), trigger: 'change' }
  ],
  memoryId: [
    { required: true, message: t('user.apply.pleaseSelectMemorySpec'), trigger: 'change' }
  ],
  diskId: [
    { required: true, message: t('user.apply.pleaseSelectDiskSpec'), trigger: 'change' }
  ],
  bandwidthId: [
    { required: true, message: t('user.apply.pleaseSelectBandwidthSpec'), trigger: 'change' }
  ]
}))

const formRef = ref()

// 格式化 CPU 规格名称以支持国际化
const formatCpuSpecName = (spec) => {
  // 如果后端返回的 name 包含"核"字，说明是硬编码的中文，需要替换
  if (spec.name && spec.name.includes('核')) {
    // 移除"核"字，只保留数字
    const coreCount = spec.cores || parseInt(spec.name)
    return `${coreCount}${t('user.apply.cores')}`
  }
  return spec.name
}

// 基于用户配额过滤的可用选项（不使用硬编码等级限制）
const availableCpuSpecs = computed(() => {
  const specs = instanceConfig.value.cpuSpecs || []
  // 格式化每个规格的名称以支持国际化
  return specs.map(spec => ({
    ...spec,
    name: formatCpuSpecName(spec)
  }))
})

// 当前选中的镜像信息
const selectedImageInfo = computed(() => {
  if (!configForm.imageId) return null
  return availableImages.value.find(img => img.id === configForm.imageId)
})

// 格式化镜像硬件要求
const formatImageRequirements = (image) => {
  if (!image.minMemoryMB || !image.minDiskMB) return ''
  const memoryMB = image.minMemoryMB
  const diskGB = Math.round(image.minDiskMB / 1024 * 10) / 10
  return `≥${memoryMB}MB / ${diskGB}GB`
}

const availableMemorySpecs = computed(() => {
  const allSpecs = instanceConfig.value.memorySpecs || []
  
  // 根据镜像的最低内存要求过滤
  if (configForm.imageId) {
    const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
    if (selectedImage && selectedImage.minMemoryMB) {
      return allSpecs.filter(spec => spec.sizeMB >= selectedImage.minMemoryMB)
    }
  }
  
  return allSpecs
})

const availableDiskSpecs = computed(() => {
  const allSpecs = instanceConfig.value.diskSpecs || []
  
  // 根据镜像的最低硬盘要求过滤
  if (configForm.imageId) {
    const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
    if (selectedImage && selectedImage.minDiskMB) {
      return allSpecs.filter(spec => spec.sizeMB >= selectedImage.minDiskMB)
    }
  }
  
  return allSpecs
})

const availableBandwidthSpecs = computed(() => {
  return instanceConfig.value.bandwidthSpecs || []
})

// 获取服务器状态类型
const getProviderStatusType = (status) => {
  switch (status) {
    case 'active':
      return 'success'
    case 'offline':
    case 'inactive':
      return 'danger'
    case 'partial':
      return 'warning'
    default:
      return 'info'
  }
}

// 获取服务器状态文本
const getProviderStatusText = (status) => {
  switch (status) {
    case 'active':
      return t('user.apply.statusActive')
    case 'offline':
    case 'inactive':
      return t('user.apply.statusOffline')
    case 'partial':
      return t('user.apply.statusPartial')
    default:
      return status
  }
}

// 格式化Provider位置信息
const formatProviderLocation = (provider) => {
  const parts = []
  if (provider.city) {
    parts.push(provider.city)
  }
  if (provider.country) {
    parts.push(provider.country)
  } else if (provider.region) {
    parts.push(provider.region)
  }
  return parts.length > 0 ? parts.join(', ') : '-'
}

// 检查是否可以选择指定规格（所有规格都已由后端根据配额过滤）
const canSelectSpec = (specType, spec) => {
  // 所有返回的规格都已经通过后端配额验证，都可以选择
  return true
}

// 检查是否可以创建指定类型的实例
const canCreateInstanceType = (instanceType) => {
  if (!selectedProvider.value) return false
  
  const capabilities = providerCapabilities.value[selectedProvider.value.id]
  if (!capabilities) return false
  
  // 检查服务器是否支持该实例类型
  const supportsType = capabilities.supportedTypes?.includes(instanceType)
  if (!supportsType) return false
  
  // 使用新的权限结构检查用户配额权限
  switch (instanceType) {
    case 'container':
      return instanceTypePermissions.value.canCreateContainer
    case 'vm':
      return instanceTypePermissions.value.canCreateVM
    default:
      return false
  }
}

// 自动选择第一个可用的规格选项
const autoSelectFirstAvailableSpecs = () => {
  // 自动选择第一个可用的CPU规格
  if (availableCpuSpecs.value.length > 0 && !configForm.cpuId) {
    configForm.cpuId = availableCpuSpecs.value[0].id
  }
  
  // 自动选择第一个可用的内存规格
  if (availableMemorySpecs.value.length > 0 && !configForm.memoryId) {
    configForm.memoryId = availableMemorySpecs.value[0].id
  }
  
  // 自动选择第一个可用的磁盘规格
  if (availableDiskSpecs.value.length > 0 && !configForm.diskId) {
    configForm.diskId = availableDiskSpecs.value[0].id
  }
  
  // 自动选择第一个可用的带宽规格
  if (availableBandwidthSpecs.value.length > 0 && !configForm.bandwidthId) {
    configForm.bandwidthId = availableBandwidthSpecs.value[0].id
  }
}

// 当实例类型变化时，重新加载对应的镜像
const onInstanceTypeChange = async () => {
  if (selectedProvider.value && configForm.type) {
    // 检查节点是否支持该实例类型
    if (configForm.type === 'container') {
      if (!selectedProvider.value.containerEnabled) {
        ElMessage.warning(t('user.apply.nodeNotSupportContainer'))
        configForm.type = 'vm'
        return
      }
      // 检查容器槽位
      if (selectedProvider.value.availableContainerSlots !== -1 && selectedProvider.value.availableContainerSlots <= 0) {
        ElMessage.warning(t('user.apply.nodeContainerSlotsFull'))
        // 尝试切换到虚拟机
        if (selectedProvider.value.vmEnabled && (selectedProvider.value.availableVMSlots === -1 || selectedProvider.value.availableVMSlots > 0)) {
          configForm.type = 'vm'
          ElMessage.info(t('user.apply.autoSwitchToVM'))
        } else {
          // 取消选择该节点
          selectedProvider.value = null
          ElMessage.warning(t('user.apply.nodeResourceInsufficient'))
          return
        }
      }
    } else if (configForm.type === 'vm') {
      if (!selectedProvider.value.vmEnabled) {
        ElMessage.warning(t('user.apply.nodeNotSupportVM'))
        configForm.type = 'container'
        return
      }
      // 检查虚拟机槽位
      if (selectedProvider.value.availableVMSlots !== -1 && selectedProvider.value.availableVMSlots <= 0) {
        ElMessage.warning(t('user.apply.nodeVMSlotsFull'))
        // 尝试切换到容器
        if (selectedProvider.value.containerEnabled && (selectedProvider.value.availableContainerSlots === -1 || selectedProvider.value.availableContainerSlots > 0)) {
          configForm.type = 'container'
          ElMessage.info(t('user.apply.autoSwitchToContainer'))
        } else {
          // 取消选择该节点
          selectedProvider.value = null
          ElMessage.warning(t('user.apply.nodeResourceInsufficient'))
          return
        }
      }
    }
    
    await loadFilteredImages()
  }
  // 清空已选择的镜像
  configForm.imageId = ''
  
  // 自动选择第一个可用的规格选项（内存和磁盘可能因实例类型而变化）
  autoSelectFirstAvailableSpecs()
}

// 获取可用提供商列表
const loadProviders = async (showSuccessMsg = false) => {
  try {
    loading.value = true
    const response = await getAvailableProviders()
    if (response.code === 0 || response.code === 200) {
      providers.value = response.data || []
      
      // 如果没有可用的提供商，给出更明确的提示
      if (providers.value.length === 0) {
        ElMessage.info(t('user.apply.noProvidersRetry'))
        console.info('没有可用的Provider，可能原因：资源未同步、节点离线或配置不完整')
      } else if (showSuccessMsg) {
        ElMessage.success(t('user.apply.refreshedProviders', { count: providers.value.length }))
      }
    } else {
      providers.value = []
      console.warn('获取提供商列表失败:', response.message)
      if (response.message) {
        ElMessage.warning(response.message)
      }
    }
  } catch (error) {
    console.error('获取提供商列表失败:', error)
    providers.value = []
    ElMessage.error('获取提供商列表失败，请检查网络连接')
  } finally {
    loading.value = false
  }
}

// 获取用户限制信息
const loadUserLimits = async () => {
  try {
    const response = await getUserLimits()
    if (response.code === 0 || response.code === 200) {
      Object.assign(userLimits, response.data)
    } else {
      console.warn('获取用户限制失败:', response.message)
    }
  } catch (error) {
    console.error('获取用户限制失败:', error)
  }
}

// 获取节点支持能力
const loadProviderCapabilities = async (providerId) => {
  try {
    const response = await getProviderCapabilities(providerId)
    if (response.code === 0 || response.code === 200) {
      providerCapabilities.value[providerId] = response.data
    } else {
      console.warn('获取节点支持能力失败:', response.message)
    }
  } catch (error) {
    console.error('获取节点支持能力失败:', error)
  }
}

// 获取实例配置选项
const loadInstanceConfig = async (providerId = null) => {
  try {
    const response = await getInstanceConfig(providerId)
    if (response.code === 0 || response.code === 200) {
      Object.assign(instanceConfig.value, response.data)
    } else {
      console.warn('获取实例配置失败:', response.message)
    }
  } catch (error) {
    console.error('获取实例配置失败:', error)
  }
}

// 获取实例类型权限配置
const loadInstanceTypePermissions = async () => {
  try {
    const response = await getUserInstanceTypePermissions()
    if (response.code === 0 || response.code === 200) {
      Object.assign(instanceTypePermissions.value, response.data)
    } else {
      console.warn('获取实例类型权限配置失败:', response.message)
    }
  } catch (error) {
    console.error('获取实例类型权限配置失败:', error)
  }
}

// 获取过滤后的镜像列表
const loadFilteredImages = async () => {
  if (!selectedProvider.value || !configForm.type) {
    availableImages.value = []
    return
  }
  
  try {
    const capabilities = providerCapabilities.value[selectedProvider.value.id]
    if (!capabilities) {
      await loadProviderCapabilities(selectedProvider.value.id)
    }
    
    const response = await getFilteredImages({
      provider_id: selectedProvider.value.id,
      instance_type: configForm.type,
      architecture: capabilities?.architecture || 'amd64'
    })
    
    if (response.code === 0 || response.code === 200) {
      availableImages.value = response.data || []
    } else {
      availableImages.value = []
      console.warn('获取过滤镜像失败:', response.message)
    }
  } catch (error) {
    console.error('获取过滤镜像失败:', error)
    availableImages.value = []
  }
}

// 选择节点
const selectProvider = async (provider) => {
  if (provider.status === 'offline' || provider.status === 'inactive') {
    ElMessage.warning('该节点当前离线，无法选择')
    return
  }
  
  // 检查是否有可用的实例类型
  const hasAvailableContainer = provider.containerEnabled && (provider.availableContainerSlots === -1 || provider.availableContainerSlots > 0)
  const hasAvailableVM = provider.vmEnabled && (provider.availableVMSlots === -1 || provider.availableVMSlots > 0)
  
  if (!hasAvailableContainer && !hasAvailableVM) {
    ElMessage.warning('该节点资源不足，无法创建新实例')
    return
  }
  
  selectedProvider.value = provider
  
  // 加载节点支持能力
  await loadProviderCapabilities(provider.id)
  
  // 重新加载实例配置（根据节点的等级限制过滤）
  await loadInstanceConfig(provider.id)
  
  // 检查当前选择的实例类型是否在新节点中可用
  const currentType = configForm.type
  const canUseCurrentType = canCreateInstanceType(currentType)
  
  // 如果当前类型不可用，自动切换到第一个可用的类型
  if (!canUseCurrentType) {
    const capabilities = providerCapabilities.value[provider.id]
    if (capabilities && capabilities.supportedTypes && capabilities.supportedTypes.length > 0) {
      // 按优先级顺序检查可用类型：container -> vm
      for (const type of ['container', 'vm']) {
        if (capabilities.supportedTypes.includes(type) && canCreateInstanceType(type)) {
          configForm.type = type
          break
        }
      }
    }
  }
  
  // 重新加载镜像列表
  if (configForm.type) {
    await loadFilteredImages()
  }
  
  // 清空已选择的镜像，因为不同服务器支持的镜像可能不同
  configForm.imageId = ''
  
  // 自动选择第一个可用的规格选项
  autoSelectFirstAvailableSpecs()
}

// 重置表单
const resetForm = async () => {
  if (formRef.value) {
    formRef.value.resetFields()
  }
  Object.assign(configForm, {
    type: 'container',
    imageId: '',
    cpu: 1,
    memory: 512,
    disk: 20,
    bandwidth: 100,
    description: ''
  })
  
  // 重新加载镜像
  if (selectedProvider.value) {
    await loadFilteredImages()
  }
}

// 提交申请
const submitApplication = async () => {
  // 防止重复提交：如果正在提交，直接返回
  if (submitting.value) {
    ElMessage.warning(t('user.apply.submitInProgress'))
    return
  }

  if (!selectedProvider.value) {
    ElMessage.warning(t('user.apply.pleaseSelectProvider'))
    return
  }

  // 检查实例类型是否支持
  if (!canCreateInstanceType(configForm.type)) {
    ElMessage.error(t('user.apply.instanceTypeNotSupported'))
    return
  }

  // 检查资源规格是否已选择
  if (!configForm.cpuId) {
    ElMessage.error(t('user.apply.pleaseSelectCpuSpec'))
    return
  }

  if (!configForm.memoryId) {
    ElMessage.error(t('user.apply.pleaseSelectMemorySpec'))
    return
  }

  if (!configForm.diskId) {
    ElMessage.error(t('user.apply.pleaseSelectDiskSpec'))
    return
  }

  if (!configForm.bandwidthId) {
    ElMessage.error(t('user.apply.pleaseSelectBandwidthSpec'))
    return
  }

  try {
    await formRef.value.validate()
    
    // 设置提交状态，防止重复点击
    submitting.value = true
    
    const requestData = {
      providerId: selectedProvider.value.id,
      imageId: configForm.imageId,
      cpuId: configForm.cpuId,
      memoryId: configForm.memoryId,
      diskId: configForm.diskId,
      bandwidthId: configForm.bandwidthId,
      description: configForm.description
    }
    
    const response = await createInstance(requestData)
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(t('user.apply.instanceCreatedSuccess'))
      // 显示任务信息
      if (response.data && response.data.taskId) {
        ElMessage.info(t('user.apply.taskIdInfo', { taskId: response.data.taskId }))
      }
      
      // 强制等待3秒后跳转到任务页面
      setTimeout(() => {
        router.push('/user/tasks')
      }, 3000)
    } else {
      // 检查是否是重复提交的情况
      if (response.message && response.message.includes('进行中')) {
        ElMessage.warning(t('user.apply.duplicateTaskWarning'))
        
        // 强制等待3秒后跳转到任务页面
        setTimeout(() => {
          router.push('/user/tasks')
        }, 3000)
      } else {
        ElMessage.error(response.message || t('user.apply.createInstanceFailed'))
        // 提交失败时重置提交状态
        submitting.value = false
      }
    }
  } catch (error) {
    if (error !== false) { // 表单验证失败时error为false
      console.error('提交申请失败:', error)
      if (error.message && error.message.includes('timeout')) {
        ElMessage.error(t('user.apply.requestTimeout'))
        
        // 强制等待3秒后跳转到任务页面
        setTimeout(() => {
          router.push('/user/tasks')
        }, 3000)
      } else {
        ElMessage.error(t('user.apply.submitFailed'))
        // 提交失败时重置提交状态
        submitting.value = false
      }
    } else {
      // 表单验证失败时重置提交状态
      submitting.value = false
    }
  }
  // 注意：成功提交后不在finally中重置submitting，保持按钮禁用状态直到页面跳转
}

// 监听路由变化，确保页面切换时重新加载数据
watch(() => route.path, (newPath, oldPath) => {
  if (newPath === '/user/apply' && oldPath !== newPath) {
    loadProviders()
    loadUserLimits()
    loadInstanceConfig()
  }
}, { immediate: false })

// 监听镜像选择变化，重置不符合要求的内存和磁盘选择
watch(() => configForm.imageId, (newImageId, oldImageId) => {
  if (newImageId !== oldImageId && newImageId) {
    // 获取当前选择的镜像
    const selectedImage = availableImages.value.find(img => img.id === newImageId)
    if (selectedImage && selectedImage.minMemoryMB && selectedImage.minDiskMB) {
      const minMemoryMB = selectedImage.minMemoryMB
      const minDiskMB = selectedImage.minDiskMB
      
      let needAutoSelect = false
      
      // 检查当前选择的内存是否符合新的最低要求
      if (configForm.memoryId) {
        const currentMemory = instanceConfig.value.memorySpecs?.find(spec => spec.id === configForm.memoryId)
        if (currentMemory && currentMemory.sizeMB < minMemoryMB) {
          configForm.memoryId = ''
          needAutoSelect = true
          ElMessage.warning(`镜像类型变更，当前内存规格不符合最低要求，已自动选择合适的规格`)
        }
      }
      
      // 检查当前选择的磁盘是否符合新的最低要求
      if (configForm.diskId) {
        const currentDisk = instanceConfig.value.diskSpecs?.find(spec => spec.id === configForm.diskId)
        if (currentDisk && currentDisk.sizeMB < minDiskMB) {
          configForm.diskId = ''
          needAutoSelect = true
          ElMessage.warning(`镜像类型变更，当前磁盘规格不符合最低要求，已自动选择合适的规格`)
        }
      }
      
      // 如果有规格被重置，自动选择第一个可用的
      if (needAutoSelect) {
        autoSelectFirstAvailableSpecs()
      }
    }
  }
})

// 监听Provider选择变化，重新验证规格
watch(() => selectedProvider.value?.type, (newProviderType, oldProviderType) => {
  if (newProviderType !== oldProviderType && configForm.imageId) {
    // Provider变化时，重新检查镜像的硬件要求（通过computed自动处理）
    const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
    if (selectedImage && selectedImage.minDiskMB) {
      const currentDisk = instanceConfig.value.diskSpecs?.find(spec => spec.id === configForm.diskId)
      if (currentDisk && currentDisk.sizeMB < selectedImage.minDiskMB) {
        configForm.diskId = ''
        ElMessage.warning(`Provider变更，当前磁盘规格不符合镜像的最低要求，已自动重置`)
        // 自动选择第一个可用的磁盘规格
        if (availableDiskSpecs.value.length > 0) {
          configForm.diskId = availableDiskSpecs.value[0].id
        }
      }
    }
  }
})

// 监听自定义导航事件
const handleRouterNavigation = (event) => {
  if (event.detail && event.detail.path === '/user/apply') {
    loadProviders()
    loadUserLimits()
    loadInstanceTypePermissions()
    loadInstanceConfig()
  }
}

onMounted(async () => {
  // 自定义导航事件监听器
  window.addEventListener('router-navigation', handleRouterNavigation)
  // 强制页面刷新监听器
  window.addEventListener('force-page-refresh', handleForceRefresh)
  
  // 优先加载核心数据，避免并发请求导致数据库锁定
  try {
    // 首先加载用户权限信息
    await loadInstanceTypePermissions()
    
    // 然后加载提供商列表
    await loadProviders()
    
    // 最后异步加载其他辅助数据
    Promise.allSettled([
      loadInstanceConfig(),
      loadUserLimits()
    ])
  } catch (error) {
    console.error('页面初始化失败:', error)
    ElMessage.error('页面加载失败，请稍后重试')
  }
})

// 使用 onActivated 确保每次页面激活时都重新加载数据
onActivated(async () => {
  // 避免并发请求，按优先级顺序加载
  try {
    await loadInstanceTypePermissions()
    await loadProviders()
    
    // 异步加载其他数据
    Promise.allSettled([
      loadInstanceConfig(),
      loadUserLimits()
    ])
  } catch (error) {
    console.error('页面激活时数据加载失败:', error)
  }
})

// 处理强制刷新事件
const handleForceRefresh = async (event) => {
  if (event.detail && event.detail.path === '/user/apply') {
    // 避免并发请求
    try {
      await loadInstanceTypePermissions()
      await loadProviders()
      
      Promise.allSettled([
        loadInstanceConfig(),
        loadUserLimits()
      ])
    } catch (error) {
      console.error('强制刷新时数据加载失败:', error)
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
  color: #1f2937;
}

.page-header p {
  margin: 0;
  color: #6b7280;
}

.user-limits-card,
.providers-card,
.config-card {
  margin-bottom: 24px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
  color: #1f2937;
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
  background: #f9fafb;
  border-radius: 8px;
}

.limit-item .label {
  color: #6b7280;
  font-weight: 500;
}

.limit-item .value {
  color: #1f2937;
  font-weight: 600;
}

.providers-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 16px;
}

.provider-card {
  border: 2px solid #e5e7eb;
  border-radius: 12px;
  padding: 16px;
  cursor: pointer;
  transition: all 0.3s ease;
  background-color: #ffffff;
}

.provider-card:hover {
  border-color: #3b82f6;
  box-shadow: 0 4px 12px rgba(59, 130, 246, 0.15);
  transform: translateY(-2px);
}

.provider-card.selected {
  border-color: #3b82f6;
  background-color: #eff6ff;
  box-shadow: 0 4px 16px rgba(59, 130, 246, 0.2);
}

/* Active状态 - 绿色 */
.provider-card.active {
  border-color: #10b981;
  background-color: #f0fdf4;
}

.provider-card.active:hover {
  border-color: #059669;
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.2);
}

.provider-card.active.selected {
  border-color: #059669;
  background-color: #dcfce7;
  box-shadow: 0 4px 16px rgba(16, 185, 129, 0.25);
}

/* Offline状态 - 红色 */
.provider-card.offline {
  border-color: #ef4444;
  background-color: #fef2f2;
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
  background-color: #fffbeb;
}

.provider-card.partial:hover {
  border-color: #d97706;
  box-shadow: 0 4px 12px rgba(245, 158, 11, 0.2);
}

.provider-card.partial.selected {
  border-color: #d97706;
  background-color: #fef3c7;
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
  color: #1f2937;
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
  color: #6b7280;
}

.loading-container {
  padding: 24px;
}
</style>

<template>
  <div class="user-apply">
    <div class="page-header">
      <h1>申请领取</h1>
      <p>选择服务器并配置您的虚拟机或容器实例</p>
    </div>

    <!-- 用户等级和限制信息 -->
    <el-card class="user-limits-card">
      <template #header>
        <div class="card-header">
          <span>用户配额信息</span>
        </div>
      </template>
      <div class="limits-grid">
        <div class="limit-item">
          <span class="label">最大实例数</span>
          <span class="value">{{ userLimits.usedInstances }} / {{ userLimits.maxInstances }}</span>
        </div>
        <div class="limit-item">
          <span class="label">CPU核心限制</span>
          <span class="value">{{ userLimits.usedCpu }} / {{ userLimits.maxCpu }}核</span>
        </div>
        <div class="limit-item">
          <span class="label">内存限制</span>
          <span class="value">{{ formatResourceUsage(userLimits.usedMemory, userLimits.maxMemory, 'memory') }}</span>
        </div>
        <div class="limit-item">
          <span class="label">硬盘限制</span>
          <span class="value">{{ formatResourceUsage(userLimits.usedDisk, userLimits.maxDisk, 'disk') }}</span>
        </div>
        <div class="limit-item">
          <span class="label">流量限制</span>
          <span class="value">{{ formatResourceUsage(userLimits.usedTraffic, userLimits.maxTraffic, 'disk') }}</span>
        </div>
      </div>
    </el-card>

    <!-- 服务器选择 -->
    <el-card class="providers-card">
      <template #header>
        <div class="card-header">
          <span>选择服务器</span>
          <el-button
            size="small"
            @click="() => loadProviders(true)"
          >
            <el-icon><Refresh /></el-icon>
            刷新
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
                位置: {{ provider.country || provider.region || '-' }}
              </span>
            </div>
            <div class="info-item">
              <span>CPU: {{ provider.cpu }}核</span>
            </div>
            <div class="info-item">
              <span>内存: {{ formatMemorySize(provider.memory || 0) }}</span>
            </div>
            <div class="info-item">
              <span>硬盘: {{ formatDiskSize(provider.disk || 0) }}</span>
            </div>
            <div 
              v-if="provider.containerEnabled && provider.vmEnabled"
              class="info-item"
            >
              <span>
                可用实例: 
                容器{{ provider.availableContainerSlots === -1 ? '不限制' : provider.availableContainerSlots }} / 
                虚拟机{{ provider.availableVMSlots === -1 ? '不限制' : provider.availableVMSlots }}
              </span>
            </div>
            <div 
              v-else-if="provider.containerEnabled"
              class="info-item"
            >
              <span>可用容器: {{ provider.availableContainerSlots === -1 ? '不限制' : provider.availableContainerSlots }}</span>
            </div>
            <div 
              v-else-if="provider.vmEnabled"
              class="info-item"
            >
              <span>可用虚拟机: {{ provider.availableVMSlots === -1 ? '不限制' : provider.availableVMSlots }}</span>
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
          <span>配置实例 - {{ selectedProvider.name }}</span>
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
              label="实例类型"
              prop="type"
            >
              <el-select
                v-model="configForm.type"
                placeholder="选择实例类型"
                @change="onInstanceTypeChange"
              >
                <el-option 
                  label="容器" 
                  value="container" 
                  :disabled="!canCreateInstanceType('container')"
                />
                <el-option 
                  label="虚拟机" 
                  value="vm" 
                  :disabled="!canCreateInstanceType('vm')"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="系统镜像"
              prop="imageId"
            >
              <el-select
                v-model="configForm.imageId"
                placeholder="选择系统镜像"
              >
                <el-option 
                  v-for="image in availableImages" 
                  :key="image.id" 
                  :label="image.name" 
                  :value="image.id"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              label="CPU规格"
              prop="cpuId"
            >
              <el-select
                v-model="configForm.cpuId"
                placeholder="选择CPU规格"
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
              label="内存规格"
              prop="memoryId"
            >
              <el-select
                v-model="configForm.memoryId"
                placeholder="选择内存规格"
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
              label="磁盘规格"
              prop="diskId"
            >
              <el-select
                v-model="configForm.diskId"
                placeholder="选择磁盘规格"
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
              label="带宽规格"
              prop="bandwidthId"
            >
              <el-select
                v-model="configForm.bandwidthId"
                placeholder="选择带宽规格"
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

        <el-form-item label="备注说明">
          <el-input 
            v-model="configForm.description"
            type="textarea"
            :rows="3"
            placeholder="请输入备注说明（可选）"
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
            提交申请
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
      description="暂无可用服务器节点"
    >
      <template #description>
        <p>当前没有可用的服务器节点</p>
        <p style="font-size: 12px; color: #909399; margin-top: 8px;">
          可能原因：节点资源未同步、服务器离线或配置不完整
        </p>
      </template>
      <el-button
        type="primary"
        @click="() => loadProviders(true)"
      >
        刷新
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

const configRules = {
  type: [
    { required: true, message: '请选择实例类型', trigger: 'change' }
  ],
  imageId: [
    { required: true, message: '请选择系统镜像', trigger: 'change' }
  ],
  cpuId: [
    { required: true, message: '请选择CPU规格', trigger: 'change' }
  ],
  memoryId: [
    { required: true, message: '请选择内存规格', trigger: 'change' }
  ],
  diskId: [
    { required: true, message: '请选择磁盘规格', trigger: 'change' }
  ],
  bandwidthId: [
    { required: true, message: '请选择带宽规格', trigger: 'change' }
  ]
}

const formRef = ref()

// 基于用户配额过滤的可用选项（不使用硬编码等级限制）
const availableCpuSpecs = computed(() => {
  return instanceConfig.value.cpuSpecs || []
})

const availableMemorySpecs = computed(() => {
  const allSpecs = instanceConfig.value.memorySpecs || []
  
  // 根据实例类型过滤
  if (configForm.imageId) {
    const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
    if (selectedImage) {
      let minMemoryMB = 128 // 容器默认128MB
      
      if (configForm.type === 'vm') {
        minMemoryMB = 512 // 虚拟机统一512MB
      }
      
      return allSpecs.filter(spec => spec.sizeMB >= minMemoryMB)
    }
  }
  
  return allSpecs
})

const availableDiskSpecs = computed(() => {
  const allSpecs = instanceConfig.value.diskSpecs || []
  
  // 根据实例类型和Provider类型过滤
  if (configForm.imageId && selectedProvider.value) {
    const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
    if (selectedImage) {
      let minDiskMB = 1024 // 默认1GB
      
      if (configForm.type === 'vm') {
        minDiskMB = 5120 // 虚拟机统一5GB
      } else if (configForm.type === 'container') {
        if (selectedProvider.value.type === 'proxmox') {
          minDiskMB = 4096 // Proxmox容器4GB
        } else {
          minDiskMB = 1024 // 其他容器1GB
        }
      }
      
      return allSpecs.filter(spec => spec.sizeMB >= minDiskMB)
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
      return '在线'
    case 'offline':
    case 'inactive':
      return '离线'
    case 'partial':
      return '部分在线'
    default:
      return status
  }
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
        ElMessage.warning('该节点不支持容器类型')
        configForm.type = 'vm'
        return
      }
      // 检查容器槽位
      if (selectedProvider.value.availableContainerSlots !== -1 && selectedProvider.value.availableContainerSlots <= 0) {
        ElMessage.warning('该节点容器槽位不足')
        // 尝试切换到虚拟机
        if (selectedProvider.value.vmEnabled && (selectedProvider.value.availableVMSlots === -1 || selectedProvider.value.availableVMSlots > 0)) {
          configForm.type = 'vm'
          ElMessage.info('已自动切换到虚拟机类型')
        } else {
          // 取消选择该节点
          selectedProvider.value = null
          ElMessage.warning('该节点资源不足，请选择其他节点')
          return
        }
      }
    } else if (configForm.type === 'vm') {
      if (!selectedProvider.value.vmEnabled) {
        ElMessage.warning('该节点不支持虚拟机类型')
        configForm.type = 'container'
        return
      }
      // 检查虚拟机槽位
      if (selectedProvider.value.availableVMSlots !== -1 && selectedProvider.value.availableVMSlots <= 0) {
        ElMessage.warning('该节点虚拟机槽位不足')
        // 尝试切换到容器
        if (selectedProvider.value.containerEnabled && (selectedProvider.value.availableContainerSlots === -1 || selectedProvider.value.availableContainerSlots > 0)) {
          configForm.type = 'container'
          ElMessage.info('已自动切换到容器类型')
        } else {
          // 取消选择该节点
          selectedProvider.value = null
          ElMessage.warning('该节点资源不足，请选择其他节点')
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
        ElMessage.info('当前没有可用的服务器节点，请稍后再试或联系管理员')
        console.info('没有可用的Provider，可能原因：资源未同步、节点离线或配置不完整')
      } else if (showSuccessMsg) {
        ElMessage.success(`已刷新，共 ${providers.value.length} 个可用服务器`)
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
  if (!selectedProvider.value) {
    ElMessage.warning('请先选择服务器')
    return
  }

  // 检查实例类型是否支持
  if (!canCreateInstanceType(configForm.type)) {
    ElMessage.error('该服务器不支持所选实例类型或用户等级不足')
    return
  }

  // 检查资源规格是否已选择
  if (!configForm.cpuId) {
    ElMessage.error('请选择CPU规格')
    return
  }

  if (!configForm.memoryId) {
    ElMessage.error('请选择内存规格')
    return
  }

  if (!configForm.diskId) {
    ElMessage.error('请选择磁盘规格')
    return
  }

  if (!configForm.bandwidthId) {
    ElMessage.error('请选择带宽规格')
    return
  }

  try {
    await formRef.value.validate()
    
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
      ElMessage.success('实例创建申请已提交，正在后台处理...')
      // 显示任务信息
      if (response.data && response.data.taskId) {
        ElMessage.info(`任务ID: ${response.data.taskId}，您可以在任务管理页面查看进度`)
      }
      // 导航到任务页面
      router.push('/user/tasks')
    } else {
      // 检查是否是重复提交的情况
      if (response.message && response.message.includes('进行中')) {
        ElMessage.warning('您已有实例创建任务正在进行中，请稍后再试或查看任务页面')
        router.push('/user/tasks')
      } else {
        ElMessage.error(response.message || '创建实例失败')
      }
    }
  } catch (error) {
    if (error !== false) { // 表单验证失败时error为false
      console.error('提交申请失败:', error)
      if (error.message && error.message.includes('timeout')) {
        ElMessage.error('请求超时，请稍后重试或查看任务页面')
        router.push('/user/tasks')
      } else {
        ElMessage.error('提交申请失败，请稍后重试')
      }
    }
  } finally {
    submitting.value = false
  }
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
    if (selectedImage) {
      let minMemoryMB = 128 // 默认最低内存（容器）
      let minDiskMB = 1024 // 默认最低硬盘（容器1GB）
      
      if (configForm.type === 'vm') {
        // 虚拟机类型：统一要求
        minMemoryMB = 512
        minDiskMB = 5120
      } else if (configForm.type === 'container' && selectedProvider.value) {
        // 容器类型：根据Provider类型判断硬盘要求
        minMemoryMB = 128
        if (selectedProvider.value.type === 'proxmox') {
          minDiskMB = 4096 // Proxmox容器需要4GB
        } else {
          minDiskMB = 1024 // 其他Provider容器需要1GB
        }
      }
      
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

// 监听Provider选择变化，检查容器磁盘要求变化
watch(() => selectedProvider.value?.type, (newProviderType, oldProviderType) => {
  if (newProviderType !== oldProviderType && configForm.type === 'container' && configForm.diskId) {
    // 检查当前选择的磁盘是否符合新Provider的最低要求
    let minDiskMB = 1024 // 默认容器最低硬盘1GB
    
    if (newProviderType === 'proxmox') {
      minDiskMB = 4096 // Proxmox容器需要4GB
    }
    
    const currentDisk = instanceConfig.value.diskSpecs?.find(spec => spec.id === configForm.diskId)
    if (currentDisk && currentDisk.sizeMB < minDiskMB) {
      configForm.diskId = ''
      ElMessage.warning(`Provider变更，当前磁盘规格不符合新Provider的最低要求，已自动选择合适的规格`)
      // 自动选择第一个可用的磁盘规格
      if (availableDiskSpecs.value.length > 0) {
        configForm.diskId = availableDiskSpecs.value[0].id
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

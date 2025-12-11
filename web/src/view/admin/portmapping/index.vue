<template>
  <div>
    <el-card class="box-card">
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.portMapping.title') }}</span>
          <div class="header-actions">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              style="margin-right: 10px;"
            >
              <template #title>
                <span style="font-size: 12px;">
                  {{ $t('admin.portMapping.rangePortInfo') }}
                </span>
              </template>
            </el-alert>
            <el-button
              type="primary"
              @click="openAddDialog"
            >
              <el-icon><Plus /></el-icon>
              {{ $t('admin.portMapping.addManualPort') }}
            </el-button>
            <el-button
              v-if="selectedPortMappings.length > 0"
              type="danger"
              @click="batchDeleteDirect"
            >
              {{ $t('admin.portMapping.batchDelete') }} ({{ selectedPortMappings.length }})
            </el-button>
          </div>
        </div>
      </template>
      
      <!-- 搜索和筛选 -->
      <div class="search-bar">
        <el-row :gutter="12">
          <el-col :span="5">
            <el-input 
              v-model="searchForm.keyword" 
              :placeholder="$t('admin.portMapping.searchInstance')"
              clearable
              @keyup.enter="searchPortMappings"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.providerId"
              :placeholder="$t('admin.portMapping.selectProvider')"
              clearable
              style="width: 100%;"
            >
              <el-option
                v-for="provider in providers"
                :key="provider.id"
                :label="provider.name"
                :value="provider.id"
              />
            </el-select>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.protocol"
              :placeholder="$t('admin.portMapping.protocol')"
              clearable
              style="width: 100%;"
            >
              <el-option
                label="TCP"
                value="tcp"
              />
              <el-option
                label="UDP"
                value="udp"
              />
              <el-option
                label="TCP/UDP"
                value="both"
              />
            </el-select>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.status"
              :placeholder="$t('common.status')"
              clearable
              style="width: 100%;"
            >
              <el-option
                :label="$t('admin.portMapping.statusActive')"
                value="active"
              />
              <el-option
                :label="$t('admin.portMapping.statusInactive')"
                value="inactive"
              />
            </el-select>
          </el-col>
          <el-col :span="7">
            <el-button
              type="primary"
              @click="searchPortMappings"
            >
              {{ $t('common.search') }}
            </el-button>
            <el-button @click="resetSearch">
              {{ $t('common.reset') }}
            </el-button>
          </el-col>
        </el-row>
      </div>

      <!-- 端口映射列表 -->
      <el-table 
        v-loading="loading"
        :data="portMappings" 
        stripe
        @selection-change="handleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
          :selectable="isManualPort"
        />
        <el-table-column
          prop="id"
          label="ID"
          width="80"
        />
        <el-table-column
          prop="portType"
          :label="$t('admin.portMapping.portType')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag :type="row.portType === 'manual' ? 'warning' : 'success'">
              {{ row.portType === 'manual' ? $t('admin.portMapping.manualPort') : $t('admin.portMapping.rangePort') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="instanceName"
          :label="$t('admin.portMapping.instanceName')"
          width="150"
        />
        <el-table-column
          prop="providerName"
          label="Provider"
          width="120"
        />
        <el-table-column
          prop="publicIP"
          :label="$t('admin.portMapping.publicIP')"
          width="120"
        />
        <el-table-column
          :label="$t('admin.portMapping.publicPort')"
          width="140"
        >
          <template #default="{ row }">
            <span v-if="row.portType === 'batch' && row.portCount && row.portCount > 1">
              {{ row.hostPort }}-{{ row.hostPortEnd || (row.hostPort + row.portCount - 1) }}
              <el-tag size="small" type="info" style="margin-left: 5px;">×{{ row.portCount }}</el-tag>
            </span>
            <span v-else>{{ row.hostPort }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.portMapping.internalPort')"
          width="140"
        >
          <template #default="{ row }">
            <span v-if="row.portType === 'batch' && row.portCount && row.portCount > 1">
              {{ row.guestPort }}-{{ row.guestPortEnd || (row.guestPort + row.portCount - 1) }}
            </span>
            <span v-else>{{ row.guestPort }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="protocol"
          :label="$t('admin.portMapping.protocol')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag
              v-if="row.protocol === 'both'"
              type="info"
              size="small"
            >
              TCP/UDP
            </el-tag>
            <el-tag
              v-else-if="row.protocol === 'tcp'"
              type="success"
              size="small"
            >
              TCP
            </el-tag>
            <el-tag
              v-else-if="row.protocol === 'udp'"
              type="warning"
              size="small"
            >
              UDP
            </el-tag>
            <span v-else>{{ row.protocol }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="$t('common.description')"
          width="120"
        />
        <el-table-column
          prop="isIPv6"
          label="IPv6"
          width="80"
        >
          <template #default="{ row }">
            <el-tag :type="row.isIPv6 ? 'success' : 'info'">
              {{ row.isIPv6 ? $t('common.yes') : $t('common.no') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          :label="$t('common.status')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag 
              v-if="row.status === 'active'" 
              type="success"
            >
              {{ $t('admin.portMapping.statusActive') }}
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'creating' || row.status === 'pending'" 
              type="warning"
            >
              <el-icon class="is-loading">
                <Loading />
              </el-icon>
              {{ row.status === 'creating' ? $t('admin.portMapping.statusCreating') : $t('admin.portMapping.statusPending') }}
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'deleting'" 
              type="warning"
            >
              <el-icon class="is-loading">
                <Loading />
              </el-icon>
              {{ $t('admin.portMapping.statusDeleting') }}
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'failed'" 
              type="danger"
            >
              {{ $t('admin.portMapping.statusFailed') }}
            </el-tag>
            <el-tag 
              v-else 
              type="info"
            >
              {{ row.status || $t('common.unknown') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="$t('common.createTime')"
          width="150"
        >
          <template #default="{ row }">
            {{ formatTime(row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="120"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button
              v-if="row.portType === 'manual'"
              type="danger"
              size="small"
              @click="deletePortMappingHandler(row.id)"
            >
              {{ $t('common.delete') }}
            </el-button>
            <el-tooltip
              v-else
              :content="$t('admin.portMapping.rangePortNotDeletable')"
              placement="top"
            >
              <el-button
                type="info"
                size="small"
                disabled
              >
                {{ $t('admin.portMapping.notDeletable') }}
              </el-button>
            </el-tooltip>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-container">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>

    <!-- 手动添加端口对话框 -->
    <el-dialog
      v-model="addDialogVisible"
      :title="$t('admin.portMapping.addPortDialog')"
      width="600px"
    >
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      >
        <template #title>
          <span style="font-size: 13px;">
            {{ $t('admin.portMapping.onlyLxdIncusProxmox') }}
          </span>
        </template>
      </el-alert>
      
      <el-form
        ref="addFormRef"
        :model="addForm"
        :rules="addRules"
        label-width="120px"
      >
        <el-form-item
          :label="$t('admin.portMapping.selectInstance')"
          prop="instanceId"
        >
          <el-select
            v-model="addForm.instanceId"
            :placeholder="$t('admin.portMapping.searchInstancePlaceholder')"
            filterable
            clearable
            style="width: 100%"
            :filter-method="filterInstances"
            :no-data-text="instances.length === 0 ? $t('admin.portMapping.noInstanceData') : $t('admin.portMapping.noMatchingInstance')"
            popper-class="instance-select-dropdown"
            @change="onInstanceChange"
          >
            <el-option
              v-for="instance in filteredInstances"
              :key="instance.id"
              :label="`${instance.name || instance.id} - ${getInstanceProviderType(instance) || instance.providerName || 'unknown'}`"
              :value="instance.id"
            >
              <div style="display: flex; justify-content: space-between; align-items: center;">
                <span>
                  <strong>{{ instance.name || instance.id }}</strong>
                  <span style="color: #909399; font-size: 12px; margin-left: 8px;">ID: {{ instance.id }}</span>
                </span>
                <span style="display: flex; align-items: center; gap: 8px;">
                  <el-tag 
                    :type="getProviderTagType(getInstanceProviderType(instance))" 
                    size="small"
                  >
                    {{ getInstanceProviderType(instance) || instance.providerName || 'unknown' }}
                  </el-tag>
                  <el-tag 
                    v-if="instance.status"
                    :type="instance.status === 'running' ? 'success' : 'info'" 
                    size="small"
                  >
                    {{ instance.status }}
                  </el-tag>
                </span>
              </div>
            </el-option>
          </el-select>
          <div style="color: #909399; font-size: 12px; margin-top: 5px;">
            <span v-if="filteredInstancesCount > 0">
              {{ $t('admin.portMapping.totalInstancesFound') }} <strong>{{ filteredInstancesCount }}</strong> {{ $t('admin.portMapping.availableInstances') }}
              <span v-if="filteredInstancesCount > 10">{{ $t('admin.portMapping.showingFirst10') }}</span>
            </span>
            <span
              v-else-if="supportedInstances.length === 0 && instances.length > 0"
              style="color: #e6a23c;"
            >
              ⚠️ {{ $t('admin.portMapping.noSupportedInstances') }}（{{ $t('admin.portMapping.instancesLoadedButNotSupported', { count: instances.length }) }}）
            </span>
            <span
              v-else
              style="color: #909399;"
            >
              {{ $t('admin.portMapping.pleaseSelectInstance') }}
            </span>
          </div>
          <div
            v-if="selectedInstanceProvider !== '-'"
            style="color: #67c23a; font-size: 12px; margin-top: 3px;"
          >
            {{ $t('admin.portMapping.currentInstanceProvider') }}: <strong>{{ selectedInstanceProvider }}</strong>
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.internalPort')"
          prop="guestPort"
        >
          <el-input-number
            v-model="addForm.guestPort"
            :min="1"
            :max="65535"
            :controls="false"
            :placeholder="$t('admin.portMapping.internalPortPlaceholder')"
            style="width: 100%"
            @change="updatePortRange"
          />
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.portCount')"
          prop="portCount"
        >
          <el-input-number
            v-model="addForm.portCount"
            :min="1"
            :max="100"
            :controls="true"
            :placeholder="$t('admin.portMapping.portCountPlaceholder')"
            style="width: 100%"
            @change="updatePortRange"
          />
          <div style="color: #909399; font-size: 12px; margin-top: 5px;">
            {{ $t('admin.portMapping.portCountHint') }}
          </div>
          <div
            v-if="portRangePreview"
            style="color: #409eff; font-size: 12px; margin-top: 5px;"
          >
            <strong>{{ $t('admin.portMapping.portRangePreview') }}:</strong> {{ portRangePreview }}
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.publicPort')"
          prop="hostPort"
        >
          <div style="display: flex; gap: 10px; align-items: start;">
            <el-input-number
              v-model="addForm.hostPort"
              :min="0"
              :max="65535"
              :controls="false"
              :placeholder="$t('admin.portMapping.autoAssignPort')"
              style="flex: 1"
              @change="updatePortRange"
              @blur="checkPortAvailabilityDebounced"
            />
            <el-button
              :loading="checkingPort"
              :disabled="!addForm.hostPort || addForm.hostPort === 0"
              @click="checkPortAvailability"
            >
              {{ $t('admin.portMapping.checkPort') }}
            </el-button>
          </div>
          <div style="color: #909399; font-size: 12px; margin-top: 5px;">
            {{ $t('admin.portMapping.autoAssignPortHint') }}
          </div>
          <!-- 端口检查结果 -->
          <div
            v-if="portCheckResult"
            :style="{ color: portCheckResult.available ? '#67c23a' : '#f56c6c', fontSize: '12px', marginTop: '5px' }"
          >
            <el-icon><CircleCheck v-if="portCheckResult.available" /><CircleClose v-else /></el-icon>
            {{ portCheckResult.message }}
          </div>
          <div
            v-if="portCheckResult && portCheckResult.suggestion"
            style="color: #e6a23c; font-size: 12px; margin-top: 3px;"
          >
            💡 {{ portCheckResult.suggestion }}
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.protocol')"
          prop="protocol"
        >
          <el-radio-group v-model="addForm.protocol">
            <el-radio label="tcp">
              TCP
            </el-radio>
            <el-radio label="udp">
              UDP
            </el-radio>
            <el-radio label="both">
              TCP/UDP
            </el-radio>
          </el-radio-group>
        </el-form-item>
        
        <el-form-item
          :label="$t('common.description')"
          prop="description"
        >
          <el-input
            v-model="addForm.description"
            :placeholder="$t('admin.portMapping.descriptionPlaceholder')"
            maxlength="256"
            show-word-limit
          />
        </el-form-item>
      </el-form>
      
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="addDialogVisible = false">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="addLoading"
            @click="submitAdd"
          >
            {{ $t('admin.portMapping.confirmAdd') }}
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onUnmounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Loading, Search, CircleCheck, CircleClose } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { 
  getPortMappings, 
  createPortMapping,
  deletePortMapping, 
  batchDeletePortMappings,
  checkPortAvailable,
  getProviderList,
  getAllInstances
} from '@/api/admin'

const { t } = useI18n()

// 响应式数据
const loading = ref(false)
const portMappings = ref([])
const providers = ref([])
const instances = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const selectedPortMappings = ref([])

// 自动刷新定时器
let autoRefreshTimer = null

// 搜索表单
const searchForm = reactive({
  keyword: '',
  providerId: '',
  protocol: '',
  status: ''
})

// 端口对话框
const addDialogVisible = ref(false)
const addFormRef = ref()
const addLoading = ref(false)
const addForm = reactive({
  instanceId: '',
  guestPort: null,
  hostPort: 0,
  portCount: 1,
  protocol: 'both',
  description: ''
})

// 端口检查状态
const checkingPort = ref(false)
const portCheckResult = ref(null)
let checkPortTimeout = null

const addRules = {
  instanceId: [
    { required: true, message: t('admin.portMapping.pleaseSelectInstance'), trigger: 'change' }
  ],
  guestPort: [
    { required: true, message: t('admin.portMapping.pleaseEnterInternalPort'), trigger: 'blur' },
    { type: 'number', min: 1, max: 65535, message: t('admin.portMapping.portRangeError'), trigger: 'blur' }
  ],
  portCount: [
    { type: 'number', min: 1, max: 100, message: t('admin.portMapping.portCountRangeError'), trigger: 'blur' }
  ],
  protocol: [
    { required: true, message: t('admin.portMapping.pleaseSelectProtocol'), trigger: 'change' }
  ]
}

// 获取实例对应的 Provider 类型
// 优先通过 providerId 在已加载的 providers 列表中查找 provider.type（后端返回的 instance.provider 常为 Provider 名称而非类型）
const getInstanceProviderType = (instance) => {
  if (!instance) return null

  // 1) 优先通过 providerId 查找 providers 列表中的类型
  if (instance.providerId && providers.value.length > 0) {
    const prov = providers.value.find(p => p.id === instance.providerId)
    if (prov && prov.type) return prov.type
  }

  // 2) 如果实例对象包含明确的 type 或 providerType 字段，使用它
  if (instance.type) return instance.type
  if (instance.providerType) return instance.providerType

  // 3) 作为回退，尝试解析 instance.provider 或 instance.providerName（有可能就是类型字符串）
  if (instance.provider) {
    const lower = String(instance.provider).toLowerCase()
    if (['lxd', 'incus', 'proxmox', 'docker'].includes(lower)) return lower
    return instance.provider
  }
  if (instance.providerName) {
    const lower = String(instance.providerName).toLowerCase()
    if (['lxd', 'incus', 'proxmox', 'docker'].includes(lower)) return lower
    return instance.providerName
  }

  return null
}

// 过滤支持的实例（仅 LXD/Incus/Proxmox）
const supportedInstances = computed(() => {
  if (instances.value.length === 0) {
    return []
  }
  
  const filtered = instances.value.filter(instance => {
    const type = getInstanceProviderType(instance)?.toLowerCase()
    const supported = type === 'lxd' || type === 'incus' || type === 'proxmox'
    
    // 调试日志（可以在控制台看到）
    if (!supported && type) {
      console.log(`实例 ${instance.name || instance.id} 的类型 ${type} 不支持手动添加端口`)
    }
    
    return supported
  })
  
  console.log(`共 ${instances.value.length} 个实例，其中 ${filtered.length} 个支持手动添加端口`)
  return filtered
})

// 选中实例的 Provider 类型
const selectedInstanceProvider = computed(() => {
  if (!addForm.instanceId) return '-'
  const instance = instances.value.find(i => i.id === addForm.instanceId)
  if (!instance) return '-'
  const type = getInstanceProviderType(instance)
  return type || '-'
})

// 端口范围预览
const portRangePreview = computed(() => {
  if (!addForm.portCount || addForm.portCount <= 1) {
    return ''
  }
  
  const guestStart = addForm.guestPort || 0
  const guestEnd = guestStart + addForm.portCount - 1
  
  // 如果 hostPort 为 0 或未设置，表示自动分配
  if (!addForm.hostPort || addForm.hostPort === 0) {
    return t('admin.portMapping.guestPortRange', { start: guestStart, end: guestEnd }) + ' → ' + t('admin.portMapping.autoAssign')
  }
  
  const hostStart = addForm.hostPort
  const hostEnd = hostStart + addForm.portCount - 1
  
  return t('admin.portMapping.guestPortRange', { start: guestStart, end: guestEnd }) + ' → ' + 
         t('admin.portMapping.hostPortRange', { start: hostStart, end: hostEnd })
})

// 更新端口范围（当端口或数量变化时）
const updatePortRange = () => {
  // 清除之前的端口检查结果
  portCheckResult.value = null
  
  // 验证端口范围是否超出限制
  if (addForm.guestPort && addForm.portCount) {
    const guestEnd = addForm.guestPort + addForm.portCount - 1
    if (guestEnd > 65535) {
      ElMessage.warning(t('admin.portMapping.portRangeExceedsLimit'))
    }
  }
  
  if (addForm.hostPort && addForm.hostPort > 0 && addForm.portCount) {
    const hostEnd = addForm.hostPort + addForm.portCount - 1
    if (hostEnd > 65535) {
      ElMessage.warning(t('admin.portMapping.portRangeExceedsLimit'))
    }
  }
}

// 检查端口可用性（带防抖）
const checkPortAvailabilityDebounced = () => {
  if (checkPortTimeout) {
    clearTimeout(checkPortTimeout)
  }
  
  checkPortTimeout = setTimeout(() => {
    checkPortAvailability()
  }, 500)
}

// 检查端口可用性
const checkPortAvailability = async () => {
  if (!addForm.hostPort || addForm.hostPort === 0) {
    portCheckResult.value = null
    return
  }
  
  if (!addForm.instanceId) {
    ElMessage.warning(t('admin.portMapping.pleaseSelectInstanceFirst'))
    return
  }
  
  // 从实例中获取 providerId
  const selectedInstance = supportedInstances.value.find(inst => inst.id === addForm.instanceId)
  if (!selectedInstance || !selectedInstance.providerId) {
    ElMessage.error('无法获取实例的 Provider 信息')
    return
  }
  
  const portCount = addForm.portCount || 1
  
  checkingPort.value = true
  portCheckResult.value = null
  
  try {
    const response = await checkPortAvailable({
      providerId: selectedInstance.providerId,
      hostPort: addForm.hostPort,
      protocol: addForm.protocol,
      portCount: portCount
    })
    
    if (response.code === 0 && response.data) {
      const data = response.data
      portCheckResult.value = {
        available: data.available,
        message: data.available 
          ? (portCount > 1 
              ? t('admin.portMapping.portRangeAvailable', { start: addForm.hostPort, end: addForm.hostPort + portCount - 1 })
              : t('admin.portMapping.portAvailable', { port: addForm.hostPort }))
          : (data.unavailablePorts && data.unavailablePorts.length > 0
              ? t('admin.portMapping.portsUnavailable', { ports: data.unavailablePorts.join(', ') })
              : t('admin.portMapping.portUnavailable', { port: addForm.hostPort })),
        suggestion: data.suggestion || ''
      }
    } else {
      throw new Error(response.message || 'Check failed')
    }
  } catch (error) {
    console.error('Port check error:', error)
    portCheckResult.value = {
      available: false,
      message: t('admin.portMapping.portCheckFailed'),
      suggestion: ''
    }
  } finally {
    checkingPort.value = false
  }
}

// 实例过滤状态
const instanceFilterText = ref('')
const filteredInstancesAll = computed(() => {
  if (!instanceFilterText.value) {
    return supportedInstances.value
  }
  const searchText = instanceFilterText.value.toLowerCase()
  return supportedInstances.value.filter(instance => {
    const name = (instance.name || '').toLowerCase()
    const id = String(instance.id || '').toLowerCase()
    const providerType = (getInstanceProviderType(instance) || '').toLowerCase()
    const providerName = (instance.providerName || '').toLowerCase()
    return name.includes(searchText) || id.includes(searchText) || providerType.includes(searchText) || providerName.includes(searchText)
  })
})

// 限制显示前10个实例
const filteredInstances = computed(() => {
  return filteredInstancesAll.value.slice(0, 10)
})

// 计算总数
const filteredInstancesCount = computed(() => {
  return filteredInstancesAll.value.length
})

// 自定义过滤方法
const filterInstances = (query) => {
  instanceFilterText.value = query
}

// Provider 标签类型
const getProviderTagType = (providerType) => {
  const type = providerType?.toLowerCase()
  switch (type) {
    case 'lxd':
      return 'success'
    case 'incus':
      return 'primary'
    case 'proxmox':
      return 'warning'
    case 'docker':
      return 'info'
    default:
      return 'info'
  }
}

// 方法
const loadPortMappings = async () => {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      pageSize: pageSize.value,
      ...searchForm
    }
    const response = await getPortMappings(params)
    portMappings.value = response.data.items || []
    total.value = response.data.total || 0
    
    // 检查是否有正在创建的端口，如果有则启动自动刷新
    checkAndStartAutoRefresh()
  } catch (error) {
    ElMessage.error(t('admin.portMapping.loadListFailed'))
    console.error(error)
  } finally {
    loading.value = false
  }
}

// 检查是否需要自动刷新
const checkAndStartAutoRefresh = () => {
  // 检查是否有正在处理的端口（创建中、删除中、等待中）
  const hasProcessingPorts = portMappings.value.some(port => 
    port.status === 'creating' || port.status === 'deleting' || port.status === 'pending'
  )
  
  if (hasProcessingPorts) {
    // 如果有正在处理的端口，启动自动刷新（每5秒刷新一次）
    if (!autoRefreshTimer) {
      console.log(t('admin.portMapping.autoRefreshStarted'))
      autoRefreshTimer = setInterval(() => {
        loadPortMappings()
      }, 5000)
    }
  } else {
    // 没有正在处理的端口，停止自动刷新
    if (autoRefreshTimer) {
      console.log(t('admin.portMapping.autoRefreshStopped'))
      clearInterval(autoRefreshTimer)
      autoRefreshTimer = null
    }
  }
}

const loadProviders = async () => {
  try {
    const response = await getProviderList({ page: 1, pageSize: 1000 })
    providers.value = response.data.list || []
  } catch (error) {
    ElMessage.error(t('admin.portMapping.loadProvidersFailed'))
  }
}

const loadInstances = async () => {
  try {
    const response = await getAllInstances({ page: 1, pageSize: 1000 })
    instances.value = response.data.list || []
  } catch (error) {
    ElMessage.error(t('admin.portMapping.loadInstancesFailed'))
  }
}

const searchPortMappings = () => {
  currentPage.value = 1
  loadPortMappings()
}

const resetSearch = () => {
  Object.assign(searchForm, {
    keyword: '',
    providerId: '',
    protocol: '',
    status: ''
  })
  searchPortMappings()
}

// 判断是否可选择（仅手动添加的端口可以批量删除）
const isManualPort = (row) => {
  return row.portType === 'manual'
}

const handleSelectionChange = (selection) => {
  selectedPortMappings.value = selection
}

const deletePortMappingHandler = async (id) => {
  try {
    await ElMessageBox.confirm(
      t('admin.portMapping.deleteConfirm'), 
      t('common.warning'), 
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    
    const response = await deletePortMapping(id)
    // 后端现在返回任务ID，显示任务已创建的消息
    ElMessage.success(t('admin.portMapping.deletePortTaskCreated'))
    loadPortMappings()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || t('admin.portMapping.deletePortFailed'))
    }
  }
}

// 批量删除（仅删除手动添加的端口）
const batchDeleteDirect = async () => {
  if (selectedPortMappings.value.length === 0) {
    ElMessage.warning(t('admin.portMapping.selectPortsToDelete'))
    return
  }
  
  // 检查是否都是手动添加的端口
  const hasRangeMappedPort = selectedPortMappings.value.some(item => item.portType !== 'manual')
  if (hasRangeMappedPort) {
    ElMessage.warning(t('admin.portMapping.onlyManualPortsCanDelete'))
    return
  }
  
  try {
    await ElMessageBox.confirm(
      t('admin.portMapping.batchDeleteConfirm', { count: selectedPortMappings.value.length }), 
      t('admin.portMapping.batchDeleteTitle'), 
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    
    const ids = selectedPortMappings.value.map(item => item.id)
    const response = await batchDeletePortMappings(ids)
    
    // 后端现在返回任务IDs和可能的失败端口
    const data = response.data || {}
    const taskIds = data.taskIds || []
    const failedPorts = data.failedPorts || []
    
    if (failedPorts.length > 0) {
      // 部分成功
      ElMessage.warning(t('admin.portMapping.batchDeletePartialSuccess', { 
        success: taskIds.length, 
        failed: failedPorts.length 
      }))
    } else {
      // 全部成功
      ElMessage.success(t('admin.portMapping.batchDeleteTasksCreated', { count: taskIds.length }))
    }
    
    selectedPortMappings.value = []
    loadPortMappings()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || t('admin.portMapping.batchDeleteFailed'))
    }
  }
}

const handleSizeChange = (val) => {
  pageSize.value = val
  loadPortMappings()
}

const handleCurrentChange = (val) => {
  currentPage.value = val
  loadPortMappings()
}

const formatTime = (time) => {
  if (!time) return ''
  return new Date(time).toLocaleString()
}

// 打开添加端口对话框
const openAddDialog = async () => {
  // 重置表单
  Object.assign(addForm, {
    instanceId: '',
    guestPort: null,
    hostPort: 0,
    portCount: 1,
    protocol: 'both',
    description: ''
  })
  
  // 重置端口检查状态
  portCheckResult.value = null
  checkingPort.value = false
  
  // 如果实例列表为空，重新加载
  if (instances.value.length === 0) {
    await loadInstances()
  }
  
  if (supportedInstances.value.length === 0) {
    ElMessage.warning(t('admin.portMapping.noSupportedInstances'))
  }
  
  addDialogVisible.value = true
}

// 实例变化时的处理
const onInstanceChange = () => {
  // 可以在这里添加一些逻辑，比如显示实例的信息
}

// 提交添加端口
const submitAdd = async () => {
  if (!addFormRef.value) return
  
  try {
    await addFormRef.value.validate()
    
    // 检查选中的实例是否支持
    const instance = instances.value.find(i => i.id === addForm.instanceId)
    if (!instance) {
      ElMessage.error(t('admin.portMapping.instanceNotFound'))
      return
    }
    
    const providerType = getInstanceProviderType(instance)?.toLowerCase()
    if (providerType === 'docker') {
      ElMessage.error(t('admin.portMapping.dockerNotSupported'))
      return
    }
    
    if (!['lxd', 'incus', 'proxmox'].includes(providerType)) {
      ElMessage.error(t('admin.portMapping.onlyLxdIncusProxmoxSupported'))
      return
    }
    
    addLoading.value = true
    
    const data = {
      instanceId: addForm.instanceId,
      guestPort: addForm.guestPort,
      hostPort: addForm.hostPort || 0,
      portCount: addForm.portCount || 1,
      protocol: addForm.protocol,
      description: addForm.description
    }
    
    const response = await createPortMapping(data)
    
    // 根据端口数量显示不同的成功消息
    if (data.portCount > 1) {
      ElMessage.success(t('admin.portMapping.batchAddPortTaskCreated', { count: data.portCount }))
    } else {
      ElMessage.success(t('admin.portMapping.addPortTaskCreated'))
    }
    
    addDialogVisible.value = false
    loadPortMappings()
  } catch (error) {
    ElMessage.error(error.message || t('admin.portMapping.addPortFailed'))
  } finally {
    addLoading.value = false
  }
}

// 生命周期
// 生命周期
onMounted(() => {
  loadProviders()
  loadInstances()
  loadPortMappings()
})

onUnmounted(() => {
  // 清理定时器
  if (autoRefreshTimer) {
    clearInterval(autoRefreshTimer)
    autoRefreshTimer = null
  }
})
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: #303133;
  }
}

.header-actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.search-bar {
  margin-bottom: 20px;
}

.pagination-container {
  margin-top: 20px;
  text-align: right;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>

<style>
/* 实例选择下拉菜单样式 - 全局样式 */
.instance-select-dropdown {
  max-height: 400px !important;
}

.instance-select-dropdown .el-select-dropdown__list {
  max-height: 380px !important;
}
</style>

<template>
  <div class="redemption-codes-page">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.redemptionCodes.title') }}</span>
          <el-button type="primary" @click="openCreateDialog">
            {{ t('admin.redemptionCodes.batchCreate') }}
          </el-button>
        </div>
      </template>

      <!-- 过滤栏 -->
      <el-row :gutter="12" class="filter-bar">
        <el-col :span="6">
          <el-input
            v-model="filterCode"
            :placeholder="t('admin.redemptionCodes.searchCode')"
            clearable
            @change="handleFilterChange"
          />
        </el-col>
        <el-col :span="5">
          <el-select
            v-model="filterStatus"
            :placeholder="t('admin.redemptionCodes.filterStatus')"
            clearable
            @change="handleFilterChange"
          >
            <el-option value="" :label="t('admin.redemptionCodes.allStatus')" />
            <el-option value="pending_create" :label="t('admin.redemptionCodes.statusPendingCreate')" />
            <el-option value="creating" :label="t('admin.redemptionCodes.statusCreating')" />
            <el-option value="pending_use" :label="t('admin.redemptionCodes.statusPendingUse')" />
            <el-option value="used" :label="t('admin.redemptionCodes.statusUsed')" />
            <el-option value="deleting" :label="t('admin.redemptionCodes.statusDeleting')" />
          </el-select>
        </el-col>
        <el-col :span="5">
          <el-select
            v-model="filterProvider"
            :placeholder="t('admin.redemptionCodes.filterProvider')"
            clearable
            @change="handleFilterChange"
          >
            <el-option value="" :label="t('admin.redemptionCodes.allProviders')" />
            <el-option
              v-for="p in allProviders"
              :key="p.id"
              :value="p.id"
              :label="p.name"
            />
          </el-select>
        </el-col>
      </el-row>

      <!-- 批量操作栏 -->
      <div v-if="selectedRows.length > 0" class="batch-actions">
        <span style="margin-right: 12px">{{ selectedRows.length }} {{ t('common.selected') }}&nbsp;</span>
        <el-button type="primary" size="small" @click="handleExport">
          {{ t('admin.redemptionCodes.export') }}
        </el-button>
        <el-button type="danger" size="small" @click="handleBatchDelete">
          {{ t('admin.redemptionCodes.batchDelete') }}
        </el-button>
      </div>

      <!-- 表格 -->
      <el-table
        v-loading="loading"
        :data="tableData"
        @selection-change="handleSelectionChange"
      >
        <el-table-column type="selection" width="50" />
        <el-table-column prop="code" :label="t('admin.redemptionCodes.colCode')" min-width="160" />
        <el-table-column :label="t('admin.redemptionCodes.colStatus')" width="110">
          <template #default="scope">
            <el-tag :type="statusTagType(scope.row.status)" size="small">
              {{ statusLabel(scope.row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="providerName" :label="t('admin.redemptionCodes.colProvider')" width="120" />
        <el-table-column :label="t('admin.redemptionCodes.colInstanceType')" width="100">
          <template #default="scope">
            {{ scope.row.instanceType === 'container' ? t('admin.redemptionCodes.container') : t('admin.redemptionCodes.vm') }}
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.redemptionCodes.colCreationMode')" width="110">
          <template #default="scope">
            <el-tag
              v-if="scope.row.creationMode === 'copy'"
              type="warning"
              size="small"
            >
              {{ t('admin.redemptionCodes.modeCopy') }}
            </el-tag>
            <el-tag
              v-else
              type="info"
              size="small"
            >
              {{ t('admin.redemptionCodes.modeStandard') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.redemptionCodes.colSpecs')" min-width="200">
          <template #default="scope">
            <span v-if="scope.row.cpuName || scope.row.memoryName">
              CPU: {{ scope.row.cpuName || scope.row.cpuId }} / {{ t('admin.redemptionCodes.memory') }}: {{ scope.row.memoryName || scope.row.memoryId }}
              <span v-if="scope.row.diskName || scope.row.diskId"> / {{ t('admin.redemptionCodes.disk') }}: {{ scope.row.diskName || scope.row.diskId }}</span>
              <span v-if="scope.row.bandwidthName || scope.row.bandwidthId"> / {{ t('admin.redemptionCodes.bandwidth') }}: {{ scope.row.bandwidthName || scope.row.bandwidthId }}</span>
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="createdByUser" :label="t('admin.redemptionCodes.colCreatedBy')" width="110" />
        <el-table-column prop="instanceName" :label="t('admin.redemptionCodes.colInstanceName')" min-width="120">
          <template #default="scope">
            {{ scope.row.instanceName || '-' }}
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.redemptionCodes.colCreatedAt')" width="160">
          <template #default="scope">
            {{ scope.row.createdAt ? new Date(scope.row.createdAt).toLocaleString() : '' }}
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.redemptionCodes.colRedeemedAt')" width="160">
          <template #default="scope">
            {{ scope.row.redeemedAt ? new Date(scope.row.redeemedAt).toLocaleString() : '-' }}
          </template>
        </el-table-column>
        <el-table-column prop="remark" :label="t('admin.redemptionCodes.colRemark')" min-width="120" />
      </el-table>

      <!-- 分页 -->
      <div class="pagination-wrapper">
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

    <!-- 批量创建对话框 -->
    <el-dialog
      v-model="showCreateDialog"
      :title="t('admin.redemptionCodes.createDialogTitle')"
      width="min(560px, calc(100vw - 32px))"
      :before-close="handleCreateDialogClose"
    >
      <el-form
        ref="createFormRef"
        :model="createForm"
        :rules="createRules"
        label-width="110px"
      >
        <el-form-item :label="t('admin.redemptionCodes.colProvider')" prop="providerId">
          <el-select
            v-model="createForm.providerId"
            :placeholder="t('admin.redemptionCodes.providerPlaceholder')"
            style="width: 100%"
            @change="onProviderChange"
          >
            <el-option
              v-for="p in allProviders"
              :key="p.id"
              :value="p.id"
              :label="p.name"
            />
          </el-select>
        </el-form-item>
        <!-- 创建模式（仅 lxd/incus 节点显示） -->
        <el-form-item
          v-if="isLxdIncusProvider"
          :label="t('admin.redemptionCodes.creationMode')"
        >
          <el-radio-group v-model="createForm.creationMode">
            <el-radio value="standard">{{ t('admin.redemptionCodes.modeStandard') }}</el-radio>
            <el-radio value="copy">{{ t('admin.redemptionCodes.modeCopy') }}</el-radio>
          </el-radio-group>
          <div style="font-size: 12px; color: #909399; margin-top: 4px;">
            {{ t('admin.redemptionCodes.copyModeTip') }}
          </div>
        </el-form-item>
        <!-- 源容器（仅复制模式显示） -->
        <el-form-item
          v-if="isLxdIncusProvider && createForm.creationMode === 'copy'"
          :label="t('admin.redemptionCodes.sourceContainer')"
          prop="sourceContainer"
        >
          <el-select
            v-model="createForm.sourceContainer"
            :placeholder="stoppedContainersLoading ? t('admin.redemptionCodes.loadingContainers') : t('admin.redemptionCodes.sourceContainerPlaceholder')"
            style="width: 100%"
            :loading="stoppedContainersLoading"
            class="source-container-select"
            popper-class="source-container-popper"
          >
            <el-option
              v-if="stoppedContainers.length === 0 && !stoppedContainersLoading"
              value=""
              :label="t('admin.redemptionCodes.noStoppedContainers')"
              disabled
            />
            <el-option
              v-for="c in stoppedContainerOptions"
              :key="c.name"
              :value="c.name"
              :label="c.label"
            >
              <div class="source-container-option" :title="c.label">{{ c.label }}</div>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item :label="t('admin.redemptionCodes.colInstanceType')" prop="instanceType" v-if="createForm.creationMode !== 'copy'">
          <el-select
            v-model="createForm.instanceType"
            :placeholder="t('admin.redemptionCodes.instanceTypePlaceholder')"
            style="width: 100%"
            :disabled="!createForm.providerId"
            @change="onInstanceTypeChange"
          >
            <el-option
              v-if="providerCaps.containerEnabled"
              value="container"
              :label="t('admin.redemptionCodes.container')"
            />
            <el-option
              v-if="providerCaps.vmEnabled"
              value="vm"
              :label="t('admin.redemptionCodes.vm')"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('admin.redemptionCodes.colSpecs') + ' - ' + t('admin.redemptionCodes.image')" prop="imageId" v-if="createForm.creationMode !== 'copy'">
          <el-select
            v-model="createForm.imageId"
            :placeholder="t('admin.redemptionCodes.imagePlaceholder')"
            style="width: 100%"
            :disabled="!createForm.instanceType"
          >
            <el-option
              v-for="img in availableImages"
              :key="img.id"
              :value="img.id"
              :label="img.displayName || img.name"
            />
          </el-select>
        </el-form-item>
        <el-row :gutter="12" v-if="createForm.creationMode !== 'copy'">
          <el-col :span="12">
            <el-form-item label="CPU" prop="cpuId">
              <el-select
                v-model="createForm.cpuId"
                :placeholder="t('admin.redemptionCodes.cpuPlaceholder')"
                style="width: 100%"
                :disabled="!createForm.instanceType"
              >
                <el-option
                  v-for="spec in cpuSpecs"
                  :key="spec.id"
                  :value="spec.id"
                  :label="spec.name || spec.id"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item :label="t('admin.redemptionCodes.memory')" prop="memoryId">
              <el-select
                v-model="createForm.memoryId"
                :placeholder="t('admin.redemptionCodes.memoryPlaceholder')"
                style="width: 100%"
                :disabled="!createForm.instanceType"
              >
                <el-option
                  v-for="spec in memorySpecs"
                  :key="spec.id"
                  :value="spec.id"
                  :label="spec.name || spec.id"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="12" v-if="createForm.creationMode !== 'copy'">
          <el-col :span="12">
            <el-form-item :label="t('admin.redemptionCodes.disk')" prop="diskId">
              <el-select
                v-model="createForm.diskId"
                :placeholder="t('admin.redemptionCodes.diskPlaceholder')"
                style="width: 100%"
                :disabled="!createForm.instanceType"
              >
                <el-option
                  v-for="spec in diskSpecs"
                  :key="spec.id"
                  :value="spec.id"
                  :label="spec.name || spec.id"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item :label="t('admin.redemptionCodes.bandwidth')" prop="bandwidthId">
              <el-select
                v-model="createForm.bandwidthId"
                :placeholder="t('admin.redemptionCodes.bandwidthPlaceholder')"
                style="width: 100%"
                :disabled="!createForm.instanceType"
              >
                <el-option
                  v-for="spec in bandwidthSpecs"
                  :key="spec.id"
                  :value="spec.id"
                  :label="spec.name || spec.id"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <!-- GPU 直通（仅 LXD/Incus 容器，含复制模式） -->
        <el-form-item
          v-if="canConfigureGpuPassthrough"
          :label="t('admin.redemptionCodes.gpuPassthrough')"
        >
          <div class="gpu-config-wrap">
            <el-checkbox
              v-model="createForm.gpuEnabled"
              class="gpu-enable-checkbox"
            >
              {{ t('admin.redemptionCodes.gpuEnabled') }}
            </el-checkbox>
            <div v-if="createForm.gpuEnabled" class="gpu-config-panel">
              <div class="gpu-actions-row">
                <span class="gpu-actions-label">{{ t('admin.redemptionCodes.gpuDeviceIds') }}:</span>
                <el-button
                  size="small"
                  :loading="gpuDetecting"
                  @click="detectGpus"
                >
                  {{ gpuDetecting ? t('admin.redemptionCodes.gpuDetecting') : t('admin.redemptionCodes.gpuDetect') }}
                </el-button>
              </div>
              <!-- 检测结果列表 -->
              <div v-if="detectedGpus.length > 0" class="gpu-options-wrap">
                <el-checkbox-group
                  v-model="selectedGpuIndices"
                  class="gpu-options"
                >
                  <el-checkbox
                    v-for="(gpu, idx) in detectedGpus"
                    :key="idx"
                    :value="idx"
                    :label="idx"
                    class="gpu-option-item"
                  >
                    {{ t('admin.redemptionCodes.gpuDeviceIdx', { idx }) }} — {{ gpu.name || gpu.vendor || '' }}
                  </el-checkbox>
                </el-checkbox-group>
                <div class="gpu-batch-actions">
                  <el-button size="small" text @click="selectAllGpus">{{ t('admin.redemptionCodes.gpuSelectAll') }}</el-button>
                  <el-button size="small" text @click="deselectAllGpus">{{ t('admin.redemptionCodes.gpuDeselectAll') }}</el-button>
                </div>
              </div>
              <div
                v-else-if="gpuChecked && !gpuDetecting"
                class="gpu-empty-tip"
              >
                {{ t('admin.redemptionCodes.gpuNoneFound') }}
              </div>
            </div>
          </div>
        </el-form-item>
        <el-form-item :label="t('admin.redemptionCodes.countLabel')" prop="count">
          <el-input-number
            v-model="createForm.count"
            :min="1"
            :max="100"
            :controls="false"
            style="width: 140px"
          />
        </el-form-item>
        <el-form-item :label="t('admin.redemptionCodes.remarkLabel')" prop="remark">
          <el-input
            v-model="createForm.remark"
            type="textarea"
            :rows="2"
            :placeholder="t('admin.redemptionCodes.remarkPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelCreate">{{ t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="createLoading" @click="submitCreate">
            {{ t('common.create') }}
          </el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 导出对话框 -->
    <el-dialog
      v-model="showExportDialog"
      :title="t('admin.redemptionCodes.exportDialogTitle')"
      width="600px"
    >
      <!-- 步骤1：选择导出字段 -->
      <div v-if="!exportResult">
        <p style="margin-bottom: 12px; font-weight: 600;">{{ t('admin.redemptionCodes.selectExportFields') }}</p>
        <el-checkbox-group v-model="exportFields" style="margin-bottom: 16px;">
          <el-checkbox v-for="field in allExportFields" :key="field.value" :value="field.value" style="margin-bottom: 6px; width: 180px;">
            {{ field.label }}
          </el-checkbox>
        </el-checkbox-group>
        <div style="margin-bottom: 12px;">
          <el-button size="small" @click="exportFields = allExportFields.map(f => f.value)">{{ t('admin.redemptionCodes.selectAll') }}</el-button>
          <el-button size="small" @click="exportFields = []">{{ t('admin.redemptionCodes.deselectAll') }}</el-button>
        </div>
      </div>
      <!-- 步骤2：显示导出结果 -->
      <div v-else>
        <p style="margin-bottom: 8px">{{ t('admin.redemptionCodes.exportedCodes') }}</p>
        <el-input
          v-model="exportedCodesText"
          type="textarea"
          :rows="16"
          readonly
        />
      </div>
      <template #footer>
        <span class="dialog-footer" v-if="!exportResult">
          <el-button @click="showExportDialog = false">{{ t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="exportLoading" :disabled="exportFields.length === 0" @click="doExport">
            {{ t('admin.redemptionCodes.export') }}
          </el-button>
        </span>
        <span class="dialog-footer" v-else>
          <el-button @click="resetExportDialog">{{ t('common.back') }}</el-button>
          <el-button @click="showExportDialog = false; exportResult = null">{{ t('common.close') }}</el-button>
          <el-button type="primary" @click="copyExportedCodes">
            {{ t('admin.redemptionCodes.copyAll') }}
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { copyToClipboard } from '@/utils/clipboard'
import {
  getRedemptionCodes,
  batchCreateRedemptionCodes,
  exportRedemptionCodes,
  batchDeleteRedemptionCodes,
  getProviderList,
  getStoppedContainers,
  detectProviderGPUs
} from '@/api/admin'
import {
  getFilteredImages,
  getProviderCapabilities,
  getInstanceConfig
} from '@/api/user'

const { t } = useI18n()

// ── 列表状态 ──────────────────────────────────────────────
const loading = ref(false)
const tableData = ref([])
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

const filterCode = ref('')
const filterStatus = ref('')
const filterProvider = ref('')

const selectedRows = ref([])

// ── 所有节点（用于筛选 & 创建对话框） ─────────────────────
const allProviders = ref([])

// ── 创建对话框 ────────────────────────────────────────────
const showCreateDialog = ref(false)
const createLoading = ref(false)
const createFormRef = ref(null)

const createForm = reactive({
  providerId: null,
  instanceType: '',
  imageId: '',
  cpuId: '',
  memoryId: '',
  diskId: '',
  bandwidthId: '',
  count: 1,
  remark: '',
  creationMode: 'standard',
  sourceContainer: '',
  gpuEnabled: false,
  gpuDeviceIds: ''
})

const createRules = computed(() => {
  const isCopy = createForm.creationMode === 'copy'
  return {
    providerId: [{ required: true, message: t('admin.redemptionCodes.providerRequired'), trigger: 'change' }],
    instanceType: [{ required: !isCopy, message: t('admin.redemptionCodes.instanceTypeRequired'), trigger: 'change' }],
    imageId: [{ required: !isCopy, message: t('admin.redemptionCodes.imageRequired'), trigger: 'change' }],
    cpuId: [{ required: !isCopy, message: t('admin.redemptionCodes.cpuRequired'), trigger: 'change' }],
    memoryId: [{ required: !isCopy, message: t('admin.redemptionCodes.memoryRequired'), trigger: 'change' }],
    diskId: [{ required: !isCopy, message: t('admin.redemptionCodes.diskRequired'), trigger: 'change' }],
    bandwidthId: [{ required: !isCopy, message: t('admin.redemptionCodes.bandwidthRequired'), trigger: 'change' }],
    sourceContainer: [{ required: isCopy, message: t('admin.redemptionCodes.sourceContainerRequired'), trigger: 'change' }],
    count: [
      { required: true, message: t('admin.redemptionCodes.countRequired'), trigger: 'blur' },
      { type: 'number', min: 1, max: 100, message: t('admin.redemptionCodes.countRange'), trigger: 'blur' }
    ]
  }
})

// 动态规格列表
const providerCaps = reactive({ containerEnabled: false, vmEnabled: false })
const availableImages = ref([])
const cpuSpecs = ref([])
const memorySpecs = ref([])
const diskSpecs = ref([])
const bandwidthSpecs = ref([])

// 复制模式：停止中容器列表
const stoppedContainers = ref([])
const stoppedContainerOptions = ref([])
const stoppedContainersLoading = ref(false)
const isLxdIncusProvider = computed(() => {
  if (!createForm.providerId) return false
  const p = allProviders.value.find(p => p.id === createForm.providerId)
  return p && (p.type === 'lxd' || p.type === 'incus')
})

const canConfigureGpuPassthrough = computed(() => {
  if (!isLxdIncusProvider.value) return false
  return createForm.creationMode === 'copy' || createForm.instanceType === 'container'
})

// ── GPU 相关 ──────────────────────────────────────────────
const gpuDetecting = ref(false)
const gpuChecked = ref(false)
const detectedGpus = ref([])
const selectedGpuIndices = ref([])

const detectGpus = async () => {
  if (!createForm.providerId) return
  gpuDetecting.value = true
  gpuChecked.value = true
  try {
    const res = await detectProviderGPUs(createForm.providerId)
    detectedGpus.value = res.data?.gpus || res.data?.data || []
    // Auto-select all GPUs
    if (detectedGpus.value.length > 0) {
      selectedGpuIndices.value = detectedGpus.value.map((_, i) => i)
    }
  } catch (_) {
    ElMessage.warning(t('admin.redemptionCodes.gpuFetchFailed'))
  } finally {
    gpuDetecting.value = false
  }
}

const selectAllGpus = () => {
  selectedGpuIndices.value = detectedGpus.value.map((_, i) => i)
}

const deselectAllGpus = () => {
  selectedGpuIndices.value = []
}

const resetGpuSelection = () => {
  createForm.gpuEnabled = false
  createForm.gpuDeviceIds = ''
  detectedGpus.value = []
  selectedGpuIndices.value = []
  gpuChecked.value = false
}

watch(() => createForm.creationMode, (mode) => {
  if (mode === 'copy') {
    createForm.instanceType = 'container'
    createForm.imageId = ''
    createForm.cpuId = ''
    createForm.memoryId = ''
    createForm.diskId = ''
    createForm.bandwidthId = ''
    availableImages.value = []
    cpuSpecs.value = []
    memorySpecs.value = []
    diskSpecs.value = []
    bandwidthSpecs.value = []
    return
  }
  createForm.sourceContainer = ''
  if (createForm.instanceType !== 'container') {
    resetGpuSelection()
  }
})

watch(canConfigureGpuPassthrough, (canConfigure) => {
  if (!canConfigure) {
    resetGpuSelection()
  }
})

// ── 导出对话框 ─────────────────────────────────────────────
const showExportDialog = ref(false)
const exportedCodesText = ref('')
const exportResult = ref(null)
const exportLoading = ref(false)
const exportFields = ref(['code', 'status', 'provider', 'instanceType', 'cpu', 'memory', 'disk', 'bandwidth'])

const allExportFields = computed(() => [
  { value: 'code', label: t('admin.redemptionCodes.colCode') },
  { value: 'status', label: t('admin.redemptionCodes.colStatus') },
  { value: 'provider', label: t('admin.redemptionCodes.colProvider') },
  { value: 'instanceType', label: t('admin.redemptionCodes.colInstanceType') },
  { value: 'cpu', label: 'CPU' },
  { value: 'memory', label: t('admin.redemptionCodes.memory') },
  { value: 'disk', label: t('admin.redemptionCodes.disk') },
  { value: 'bandwidth', label: t('admin.redemptionCodes.bandwidth') },
  { value: 'instanceName', label: t('admin.redemptionCodes.colInstanceName') },
  { value: 'createdBy', label: t('admin.redemptionCodes.colCreatedBy') },
  { value: 'createdAt', label: t('admin.redemptionCodes.colCreatedAt') },
  { value: 'redeemedAt', label: t('admin.redemptionCodes.colRedeemedAt') },
  { value: 'remark', label: t('admin.redemptionCodes.colRemark') }
])

// ── 状态颜色 ──────────────────────────────────────────────
const statusTagType = (status) => {
  switch (status) {
    case 'pending_create': return 'info'
    case 'creating': return 'warning'
    case 'pending_use': return 'success'
    case 'used': return ''
    case 'deleting': return 'danger'
    default: return 'info'
  }
}

const statusLabel = (status) => {
  const keyMap = {
    pending_create: 'statusPendingCreate',
    creating: 'statusCreating',
    pending_use: 'statusPendingUse',
    used: 'statusUsed',
    deleting: 'statusDeleting'
  }
  return keyMap[status] ? t(`admin.redemptionCodes.${keyMap[status]}`) : status
}

// ── 数据加载 ──────────────────────────────────────────────
const loadData = async () => {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      pageSize: pageSize.value
    }
    if (filterCode.value) params.code = filterCode.value
    if (filterStatus.value) params.status = filterStatus.value
    if (filterProvider.value) params.providerId = filterProvider.value

    const res = await getRedemptionCodes(params)
    tableData.value = res.data?.list || res.data?.data || []
    total.value = res.data?.total || 0
  } catch (e) {
    ElMessage.error(e?.response?.data?.msg || e.message)
  } finally {
    loading.value = false
  }
}

const loadProviders = async () => {
  try {
    const res = await getProviderList({ page: 1, pageSize: 999 })
    allProviders.value = res.data?.list || res.data?.data || []
  } catch (_) {
    // ignore
  }
}

// ── 过滤 ───────────────────────────────────────────────────
const handleFilterChange = () => {
  currentPage.value = 1
  loadData()
}

const handleSelectionChange = (rows) => {
  selectedRows.value = rows
}

// ── 创建对话框逻辑 ─────────────────────────────────────────
const openCreateDialog = () => {
  showCreateDialog.value = true
}

const cancelCreate = () => {
  showCreateDialog.value = false
  createFormRef.value?.resetFields()
  Object.assign(createForm, {
    providerId: null,
    instanceType: '',
    imageId: '',
    cpuId: '',
    memoryId: '',
    diskId: '',
    bandwidthId: '',
    count: 1,
    remark: '',
    creationMode: 'standard',
    sourceContainer: '',
    gpuEnabled: false,
    gpuDeviceIds: ''
  })
  providerCaps.containerEnabled = false
  providerCaps.vmEnabled = false
  availableImages.value = []
  cpuSpecs.value = []
  memorySpecs.value = []
  diskSpecs.value = []
  bandwidthSpecs.value = []
  stoppedContainers.value = []
  stoppedContainerOptions.value = []
  detectedGpus.value = []
  selectedGpuIndices.value = []
  gpuChecked.value = false
}

const handleCreateDialogClose = (done) => {
  const isFormDirty = !!(createForm.providerId || createForm.instanceType || createForm.remark)
  if (isFormDirty) {
    ElMessageBox.confirm(
      t('common.unsavedChangesConfirm'),
      t('common.unsavedChanges'),
      {
        confirmButtonText: t('common.discardChanges'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    ).then(() => {
      if (typeof done === 'function') done()
      cancelCreate()
    }).catch(() => {})
  } else {
    if (typeof done === 'function') done()
    cancelCreate()
  }
}

const onProviderChange = async (providerId) => {
  // Reset dependent fields
  createForm.instanceType = ''
  createForm.imageId = ''
  createForm.cpuId = ''
  createForm.memoryId = ''
  createForm.diskId = ''
  createForm.bandwidthId = ''
  availableImages.value = []
  cpuSpecs.value = []
  memorySpecs.value = []
  diskSpecs.value = []
  bandwidthSpecs.value = []
  resetGpuSelection()

  if (!providerId) return
  try {
    const res = await getProviderCapabilities(providerId)
    const caps = res.data || {}
    providerCaps.containerEnabled = caps.containerEnabled || false
    providerCaps.vmEnabled = caps.vmEnabled || false
  } catch (_) {
    // ignore
  }

  // 如果是 lxd/incus 节点，加载已停止的容器列表和GPU列表
  const p = allProviders.value.find(p => p.id === providerId)
  if (p && (p.type === 'lxd' || p.type === 'incus')) {
    createForm.creationMode = 'standard'
    createForm.sourceContainer = ''
    stoppedContainers.value = []
    stoppedContainerOptions.value = []
    stoppedContainersLoading.value = true
    try {
      const r = await getStoppedContainers(providerId)
      const rawNames = (r.data && r.data.containers) || []
      const rawDetails = (r.data && r.data.containerDetails) || []
      stoppedContainers.value = rawNames
      if (rawDetails.length > 0) {
        stoppedContainerOptions.value = rawDetails.map(item => ({
          name: item.name,
          label: item.hasGpu ? `${item.name} [GPU${item.gpuDeviceIds ? `: ${item.gpuDeviceIds}` : ''}]` : item.name
        }))
      } else {
        stoppedContainerOptions.value = rawNames.map(name => ({ name, label: name }))
      }
    } catch (e) {
      // 区分网络/超时错误与真正的「无容器」
      const errMsg = e?.response?.data?.msg || e?.message || ''
      if (errMsg) {
        ElMessage.warning(t('admin.redemptionCodes.loadContainersFailed', { reason: errMsg }))
      }
      stoppedContainers.value = []
      stoppedContainerOptions.value = []
    } finally {
      stoppedContainersLoading.value = false
    }
    // 自动检测 GPU
    gpuDetecting.value = true
    gpuChecked.value = true
    try {
      const gpuRes = await detectProviderGPUs(providerId)
      detectedGpus.value = gpuRes.data?.gpus || gpuRes.data?.data || []
      if (detectedGpus.value.length > 0) {
        selectedGpuIndices.value = detectedGpus.value.map((_, i) => i)
      }
    } catch (_) {
      // ignore silently
    } finally {
      gpuDetecting.value = false
    }
  } else {
    createForm.creationMode = 'standard'
    createForm.sourceContainer = ''
    stoppedContainers.value = []
    stoppedContainerOptions.value = []
    resetGpuSelection()
  }
}

const onInstanceTypeChange = async (type) => {
  createForm.imageId = ''
  createForm.cpuId = ''
  createForm.memoryId = ''
  createForm.diskId = ''
  createForm.bandwidthId = ''
  availableImages.value = []
  cpuSpecs.value = []
  memorySpecs.value = []
  diskSpecs.value = []
  bandwidthSpecs.value = []

  if (type !== 'container') {
    resetGpuSelection()
  }

  if (!createForm.providerId || !type) return
  try {
    const [imgRes, cfgRes] = await Promise.all([
      getFilteredImages({ provider_id: createForm.providerId, instance_type: type }),
      getInstanceConfig(createForm.providerId)
    ])
    availableImages.value = imgRes.data || []
    const cfg = cfgRes.data || {}
    cpuSpecs.value = cfg.cpuSpecs || []
    memorySpecs.value = cfg.memorySpecs || []
    diskSpecs.value = cfg.diskSpecs || []
    bandwidthSpecs.value = cfg.bandwidthSpecs || []
    // Auto-select first options
    if (cpuSpecs.value.length) createForm.cpuId = cpuSpecs.value[0].id
    if (memorySpecs.value.length) createForm.memoryId = memorySpecs.value[0].id
    if (diskSpecs.value.length) createForm.diskId = diskSpecs.value[0].id
    if (bandwidthSpecs.value.length) createForm.bandwidthId = bandwidthSpecs.value[0].id
  } catch (_) {
    // ignore
  }
}

const submitCreate = async () => {
  try {
    await createFormRef.value.validate()
    // 额外校验复制模式下的源容器
    if (createForm.creationMode === 'copy' && !createForm.sourceContainer) {
      ElMessage.warning(t('admin.redemptionCodes.sourceContainerRequired'))
      return
    }
    if (createForm.gpuEnabled && !canConfigureGpuPassthrough.value) {
      ElMessage.warning(t('admin.redemptionCodes.gpuUnsupportedTarget'))
      return
    }
    createLoading.value = true
    await batchCreateRedemptionCodes({
      providerId: createForm.providerId,
      instanceType: createForm.creationMode === 'copy' ? 'container' : createForm.instanceType,
      imageId: createForm.creationMode === 'copy' ? 0 : createForm.imageId,
      cpuId: createForm.creationMode === 'copy' ? '' : createForm.cpuId,
      memoryId: createForm.creationMode === 'copy' ? '' : createForm.memoryId,
      diskId: createForm.creationMode === 'copy' ? '' : createForm.diskId,
      bandwidthId: createForm.creationMode === 'copy' ? '' : createForm.bandwidthId,
      count: createForm.count,
      remark: createForm.remark,
      creationMode: createForm.creationMode,
      sourceContainer: createForm.sourceContainer,
      gpuEnabled: canConfigureGpuPassthrough.value && createForm.gpuEnabled,
      gpuDeviceIds: canConfigureGpuPassthrough.value && createForm.gpuEnabled ? selectedGpuIndices.value.join(',') : ''
    })
    ElMessage.success(t('admin.redemptionCodes.createSuccess', { count: createForm.count }))
    cancelCreate()
    await loadData()
  } catch (e) {
    if (e?.response?.data?.msg) {
      ElMessage.error(e.response.data.msg || t('admin.redemptionCodes.createFailed'))
    }
    // validation errors silently ignored (form shows them)
  } finally {
    createLoading.value = false
  }
}

// ── 导出 ────────────────────────────────────────────────────
const handleExport = async () => {
  if (selectedRows.value.length === 0) {
    ElMessage.warning(t('admin.redemptionCodes.exportEmpty'))
    return
  }
  exportResult.value = null
  exportedCodesText.value = ''
  showExportDialog.value = true
}

const resetExportDialog = () => {
  exportResult.value = null
  exportedCodesText.value = ''
}

const { locale } = useI18n()

const doExport = async () => {
  exportLoading.value = true
  try {
    const ids = selectedRows.value.map(r => r.id)
    const lang = locale.value || 'zh-CN'
    const res = await exportRedemptionCodes({ ids, fields: exportFields.value, lang })
    const items = res.data?.items || []
    // 每条记录单独一行，字段用 | 分隔，记录之间用空行分隔
    exportedCodesText.value = items.map((item, idx) => {
      const fields = Object.entries(item)
        .filter(([, v]) => v !== undefined && v !== null && v !== '')
        .map(([k, v]) => `${k}: ${v}`)
        .join(' | ')
      return `[${idx + 1}] ${fields}`
    }).join('\n\n')
    exportResult.value = items
  } catch (e) {
    ElMessage.error(e?.response?.data?.msg || e.message)
  } finally {
    exportLoading.value = false
  }
}

const copyExportedCodes = async () => {
  if (!exportedCodesText.value) return
  await copyToClipboard(exportedCodesText.value, t('admin.redemptionCodes.copiedToClipboard'))
}

// ── 删除 ────────────────────────────────────────────────────
const handleBatchDelete = async () => {
  if (selectedRows.value.length === 0) {
    ElMessage.warning(t('admin.redemptionCodes.noSelection'))
    return
  }
  try {
    await ElMessageBox.confirm(
      t('admin.redemptionCodes.confirmDeleteMsg', { count: selectedRows.value.length }),
      t('admin.redemptionCodes.confirmDeleteTitle'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    const ids = selectedRows.value.map(r => r.id)
    await batchDeleteRedemptionCodes({ ids })
    ElMessage.success(t('admin.redemptionCodes.deleteSuccess'))
    selectedRows.value = []
    await loadData()
  } catch (e) {
    if (e !== 'cancel' && e?.response?.data?.msg) {
      ElMessage.error(e.response.data.msg || t('common.operationFailed'))
    }
  }
}

// ── 分页 ────────────────────────────────────────────────────
const handleSizeChange = (val) => {
  pageSize.value = val
  currentPage.value = 1
  loadData()
}

const handleCurrentChange = (val) => {
  currentPage.value = val
  loadData()
}

onMounted(async () => {
  await Promise.all([loadProviders(), loadData()])
})
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.card-header > span {
  font-size: 18px;
  font-weight: 600;
  color: var(--text-color-primary);
}
.filter-bar {
  margin-bottom: 16px;
}
.batch-actions {
  margin-bottom: 12px;
  padding: 10px 12px;
  background-color: var(--neutral-bg);
  border-radius: 4px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}
.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.gpu-config-wrap {
  width: 100%;
}

.gpu-enable-checkbox {
  margin-bottom: 8px;
}

.gpu-config-panel {
  margin-top: 8px;
}

.gpu-actions-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}

.gpu-actions-label {
  font-size: 13px;
  color: #606266;
}

.gpu-options-wrap {
  margin-bottom: 8px;
  max-height: 220px;
  overflow: auto;
  padding-right: 4px;
}

.gpu-options {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

:deep(.gpu-option-item.el-checkbox) {
  display: flex;
  align-items: flex-start;
  margin-right: 0;
  min-width: 0;
  height: auto;
}

:deep(.gpu-option-item .el-checkbox__label) {
  white-space: normal;
  line-height: 1.4;
  word-break: break-word;
  overflow-wrap: anywhere;
}

.gpu-batch-actions {
  margin-top: 4px;
  font-size: 12px;
  color: #909399;
}

.gpu-empty-tip {
  font-size: 12px;
  color: #909399;
}

:deep(.source-container-select .el-select__selected-item) {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

:deep(.source-container-popper .el-select-dropdown__item) {
  height: auto;
  min-height: 34px;
  line-height: 1.35;
  padding-top: 7px;
  padding-bottom: 7px;
}

.source-container-option {
  white-space: normal;
  word-break: break-word;
  overflow-wrap: anywhere;
}
</style>

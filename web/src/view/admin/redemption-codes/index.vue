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
        <el-table-column :label="t('admin.redemptionCodes.colSpecs')" min-width="160">
          <template #default="scope">
            <span v-if="scope.row.cpuId || scope.row.memoryId">
              CPU: {{ scope.row.cpuId }} / {{ scope.row.memoryId }}
              <span v-if="scope.row.diskId"> / {{ scope.row.diskId }}</span>
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="createdByUser" :label="t('admin.redemptionCodes.colCreatedBy')" width="110" />
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
      width="560px"
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
        <el-form-item :label="t('admin.redemptionCodes.colInstanceType')" prop="instanceType">
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
        <el-form-item :label="t('admin.redemptionCodes.colSpecs') + ' - Image'" prop="imageId">
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
        <el-row :gutter="12">
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
            <el-form-item :label="t('admin.redemptionCodes.memoryPlaceholder').replace('Select ', '')" prop="memoryId">
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
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item :label="t('admin.redemptionCodes.diskPlaceholder').replace('Select ', '')" prop="diskId">
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
            <el-form-item :label="t('admin.redemptionCodes.bandwidthPlaceholder').replace('Select ', '')" prop="bandwidthId">
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
      width="500px"
    >
      <p style="margin-bottom: 8px">{{ t('admin.redemptionCodes.exportedCodes') }}</p>
      <el-input
        v-model="exportedCodesText"
        type="textarea"
        :rows="10"
        readonly
      />
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="showExportDialog = false">{{ t('common.close') }}</el-button>
          <el-button type="primary" @click="copyExportedCodes">
            {{ t('admin.redemptionCodes.copyAll') }}
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getRedemptionCodes,
  batchCreateRedemptionCodes,
  exportRedemptionCodes,
  batchDeleteRedemptionCodes,
  getProviderList
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
  remark: ''
})

const createRules = computed(() => ({
  providerId: [{ required: true, message: t('admin.redemptionCodes.providerRequired'), trigger: 'change' }],
  instanceType: [{ required: true, message: t('admin.redemptionCodes.instanceTypeRequired'), trigger: 'change' }],
  imageId: [{ required: true, message: t('admin.redemptionCodes.imageRequired'), trigger: 'change' }],
  cpuId: [{ required: true, message: t('admin.redemptionCodes.cpuRequired'), trigger: 'change' }],
  memoryId: [{ required: true, message: t('admin.redemptionCodes.memoryRequired'), trigger: 'change' }],
  diskId: [{ required: true, message: t('admin.redemptionCodes.diskRequired'), trigger: 'change' }],
  bandwidthId: [{ required: true, message: t('admin.redemptionCodes.bandwidthRequired'), trigger: 'change' }],
  count: [
    { required: true, message: t('admin.redemptionCodes.countRequired'), trigger: 'blur' },
    { type: 'number', min: 1, max: 100, message: t('admin.redemptionCodes.countRange'), trigger: 'blur' }
  ]
}))

// 动态规格列表
const providerCaps = reactive({ containerEnabled: false, vmEnabled: false })
const availableImages = ref([])
const cpuSpecs = ref([])
const memorySpecs = ref([])
const diskSpecs = ref([])
const bandwidthSpecs = ref([])

// ── 导出对话框 ─────────────────────────────────────────────
const showExportDialog = ref(false)
const exportedCodesText = ref('')

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
    remark: ''
  })
  providerCaps.containerEnabled = false
  providerCaps.vmEnabled = false
  availableImages.value = []
  cpuSpecs.value = []
  memorySpecs.value = []
  diskSpecs.value = []
  bandwidthSpecs.value = []
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

  if (!providerId) return
  try {
    const res = await getProviderCapabilities(providerId)
    const caps = res.data || {}
    providerCaps.containerEnabled = caps.containerEnabled || false
    providerCaps.vmEnabled = caps.vmEnabled || false
  } catch (_) {
    // ignore
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
    createLoading.value = true
    await batchCreateRedemptionCodes({
      providerId: createForm.providerId,
      instanceType: createForm.instanceType,
      imageId: createForm.imageId,
      cpuId: createForm.cpuId,
      memoryId: createForm.memoryId,
      diskId: createForm.diskId,
      bandwidthId: createForm.bandwidthId,
      count: createForm.count,
      remark: createForm.remark
    })
    ElMessage.success(t('admin.redemptionCodes.createSuccess', { count: createForm.count }))
    cancelCreate()
    await loadData()
  } catch (e) {
    if (e?.response?.data?.msg) {
      ElMessage.error(e.response.data.msg)
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
  try {
    const ids = selectedRows.value.map(r => r.id)
    const res = await exportRedemptionCodes({ ids })
    exportedCodesText.value = (res.data?.codes || res.data || []).join('\n')
    showExportDialog.value = true
  } catch (e) {
    ElMessage.error(e?.response?.data?.msg || e.message)
  }
}

const copyExportedCodes = async () => {
  if (!exportedCodesText.value) return
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(exportedCodesText.value)
    } else {
      const ta = document.createElement('textarea')
      ta.value = exportedCodesText.value
      ta.style.position = 'fixed'
      ta.style.left = '-999999px'
      document.body.appendChild(ta)
      ta.focus()
      ta.select()
      // eslint-disable-next-line @typescript-eslint/no-deprecated
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    ElMessage.success(t('admin.redemptionCodes.copiedToClipboard'))
  } catch (_) {
    ElMessage.error('Copy failed')
  }
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
      ElMessage.error(e.response.data.msg)
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
  color: #303133;
}
.filter-bar {
  margin-bottom: 16px;
}
.batch-actions {
  margin-bottom: 12px;
  padding: 10px 12px;
  background-color: #f5f7fa;
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
</style>

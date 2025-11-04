<template>
  <div class="invite-codes-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.inviteCodes.title') }}</span>
          <div>
            <el-button
              type="success"
              @click="showCreateDialog = true"
            >
              {{ $t('admin.inviteCodes.createCustomCode') }}
            </el-button>
            <el-button
              type="primary"
              @click="showGenerateDialog = true"
            >
              {{ $t('admin.inviteCodes.batchGenerate') }}
            </el-button>
          </div>
        </div>
      </template>

      <!-- 筛选栏 -->
      <div class="filter-bar">
        <el-form :inline="true">
          <el-form-item :label="$t('admin.inviteCodes.usageStatus')">
            <el-select
              v-model="filterForm.isUsed"
              :placeholder="$t('common.all')"
              clearable
              style="width: 120px"
              @change="handleFilterChange"
            >
              <el-option
                :label="$t('common.all')"
                :value="null"
              />
              <el-option
                :label="$t('admin.inviteCodes.unused')"
                :value="false"
              />
              <el-option
                :label="$t('admin.inviteCodes.used')"
                :value="true"
              />
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('common.status')">
            <el-select
              v-model="filterForm.status"
              :placeholder="$t('common.all')"
              clearable
              style="width: 120px"
              @change="handleFilterChange"
            >
              <el-option
                :label="$t('common.all')"
                :value="0"
              />
              <el-option
                :label="$t('admin.inviteCodes.available')"
                :value="1"
              />
            </el-select>
          </el-form-item>
        </el-form>
      </div>

      <!-- 批量操作按钮 -->
      <div
        v-if="selectedCodes.length > 0"
        class="batch-actions"
      >
        <el-button
          type="primary"
          @click="handleBatchExport"
        >
          {{ $t('admin.inviteCodes.exportSelected') }} ({{ selectedCodes.length }})
        </el-button>
        <el-button
          type="danger"
          @click="handleBatchDelete"
        >
          {{ $t('admin.inviteCodes.deleteSelected') }} ({{ selectedCodes.length }})
        </el-button>
      </div>
      
      <el-table
        v-loading="loading"
        :data="inviteCodes"
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="id"
          label="ID"
          width="60"
        />
        <el-table-column
          prop="code"
          :label="$t('admin.inviteCodes.code')"
        />
        <el-table-column
          prop="maxUses"
          :label="$t('admin.inviteCodes.maxUses')"
          width="120"
        >
          <template #default="scope">
            {{ scope.row.maxUses === 0 ? $t('admin.inviteCodes.unlimited') : scope.row.maxUses }}
          </template>
        </el-table-column>
        <el-table-column
          prop="usedCount"
          :label="$t('admin.inviteCodes.usedCount')"
          width="120"
        />
        <el-table-column
          prop="status"
          :label="$t('common.status')"
          width="100"
        >
          <template #default="scope">
            <el-tag :type="scope.row.status === 1 ? 'success' : 'info'">
              {{ scope.row.status === 1 ? $t('admin.inviteCodes.available') : $t('admin.inviteCodes.expired') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="expiresAt"
          :label="$t('admin.inviteCodes.expiryDate')"
          width="160"
        >
          <template #default="scope">
            {{ scope.row.expiresAt ? new Date(scope.row.expiresAt).toLocaleString() : $t('admin.inviteCodes.neverExpires') }}
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="$t('common.createTime')"
          width="160"
        >
          <template #default="scope">
            {{ new Date(scope.row.createdAt).toLocaleString() }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="120"
        >
          <template #default="scope">
            <el-button
              size="small"
              type="danger"
              @click="deleteCode(scope.row.id)"
            >
              {{ $t('common.delete') }}
            </el-button>
          </template>
        </el-table-column>
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

    <!-- 创建自定义邀请码对话框 -->
    <el-dialog 
      v-model="showCreateDialog" 
      :title="$t('admin.inviteCodes.createCustomCode')" 
      width="500px"
    >
      <el-form 
        ref="createFormRef" 
        :model="createForm" 
        :rules="createRules" 
        label-width="120px"
      >
        <el-form-item
          :label="$t('admin.inviteCodes.code')"
          prop="code"
        >
          <el-input 
            v-model="createForm.code" 
            :placeholder="$t('admin.inviteCodes.codeInputPlaceholder')"
            maxlength="50"
            show-word-limit
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.codeFormatTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.maxUses')"
          prop="maxUses"
        >
          <el-input-number
            v-model="createForm.maxUses"
            :min="0"
            :controls="false"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.maxUsesTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.expiryDate')"
          prop="expiresAt"
        >
          <el-date-picker
            v-model="createForm.expiresAt"
            type="datetime"
            :placeholder="$t('admin.inviteCodes.selectExpiryDate')"
            format="YYYY-MM-DD HH:mm:ss"
            value-format="YYYY-MM-DD HH:mm:ss"
            style="width: 100%"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.expiryDateTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('common.description')"
          prop="description"
        >
          <el-input 
            v-model="createForm.description" 
            type="textarea" 
            :rows="3"
            :placeholder="$t('admin.inviteCodes.descriptionPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelCreate">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="createLoading"
            @click="submitCreate"
          >{{ $t('common.create') }}</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 生成邀请码对话框 -->
    <el-dialog 
      v-model="showGenerateDialog" 
      :title="$t('admin.inviteCodes.batchGenerate')" 
      width="500px"
    >
      <el-form 
        ref="generateFormRef" 
        :model="generateForm" 
        :rules="generateRules" 
        label-width="120px"
      >
        <el-form-item
          :label="$t('admin.inviteCodes.generateCount')"
          prop="count"
        >
          <el-input-number
            v-model="generateForm.count"
            :min="1"
            :max="100"
            :controls="false"
          />
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.maxUses')"
          prop="maxUses"
        >
          <el-input-number
            v-model="generateForm.maxUses"
            :min="0"
            :controls="false"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.maxUsesTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.expiryDate')"
          prop="expiresAt"
        >
          <el-date-picker
            v-model="generateForm.expiresAt"
            type="datetime"
            :placeholder="$t('admin.inviteCodes.selectExpiryDate')"
            format="YYYY-MM-DD HH:mm:ss"
            value-format="YYYY-MM-DD HH:mm:ss"
            style="width: 100%"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.expiryDateTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('common.description')"
          prop="description"
        >
          <el-input 
            v-model="generateForm.description" 
            type="textarea" 
            :rows="3"
            :placeholder="$t('admin.inviteCodes.descriptionPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelGenerate">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="generateLoading"
            @click="submitGenerate"
          >{{ $t('admin.inviteCodes.generate') }}</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 导出邀请码对话框 -->
    <el-dialog
      v-model="showExportDialog"
      :title="$t('admin.inviteCodes.exportCodes')"
      width="600px"
    >
      <div class="export-content">
        <el-input
          v-model="exportedCodes"
          type="textarea"
          :rows="15"
          readonly
        />
      </div>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="showExportDialog = false">{{ $t('common.close') }}</el-button>
          <el-button
            type="primary"
            @click="copyExportedCodes"
          >{{ $t('common.copy') }}</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getInviteCodes, createInviteCode, generateInviteCodes, deleteInviteCode, batchDeleteInviteCodes, exportInviteCodes } from '@/api/admin'

const { t } = useI18n()

const inviteCodes = ref([])
const loading = ref(false)
const showCreateDialog = ref(false)
const showGenerateDialog = ref(false)
const showExportDialog = ref(false)
const createLoading = ref(false)
const generateLoading = ref(false)
const createFormRef = ref()
const generateFormRef = ref()
const selectedCodes = ref([])
const exportedCodes = ref('')

// 筛选表单
const filterForm = reactive({
  isUsed: null,
  status: 0
})

// 分页
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 创建自定义邀请码表单
const createForm = reactive({
  code: '',
  maxUses: 1,
  expiresAt: '',
  description: ''
})

// 创建表单验证规则
const createRules = {
  code: [
    { required: true, message: t('admin.inviteCodes.codeRequired'), trigger: 'blur' },
    { min: 3, max: 50, message: t('admin.inviteCodes.codeLengthError'), trigger: 'blur' },
    { pattern: /^[0-9A-Z]+$/, message: t('admin.inviteCodes.codeFormatError'), trigger: 'blur' }
  ],
  maxUses: [
    { required: true, message: t('admin.inviteCodes.maxUsesRequired'), trigger: 'blur' }
  ]
}

// 生成邀请码表单
const generateForm = reactive({
  count: 1,
  maxUses: 1,
  expiresAt: '',
  description: ''
})

// 表单验证规则
const generateRules = {
  count: [
    { required: true, message: t('admin.inviteCodes.countRequired'), trigger: 'blur' }
  ],
  maxUses: [
    { required: true, message: t('admin.inviteCodes.maxUsesRequired'), trigger: 'blur' }
  ]
}

const loadInviteCodes = async () => {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      pageSize: pageSize.value
    }
    
    if (filterForm.isUsed !== null) {
      params.isUsed = filterForm.isUsed
    }
    if (filterForm.status !== 0) {
      params.status = filterForm.status
    }
    
    const response = await getInviteCodes(params)
    inviteCodes.value = response.data.list || []
    total.value = response.data.total || 0
  } catch (error) {
    ElMessage.error(t('admin.inviteCodes.loadFailed'))
  } finally {
    loading.value = false
  }
}

const handleFilterChange = () => {
  currentPage.value = 1
  loadInviteCodes()
}

const handleSelectionChange = (selection) => {
  selectedCodes.value = selection
}

const handleBatchExport = async () => {
  if (selectedCodes.value.length === 0) {
    ElMessage.warning(t('admin.inviteCodes.selectToExport'))
    return
  }
  
  try {
    const ids = selectedCodes.value.map(item => item.id)
    const response = await exportInviteCodes({ ids })
    exportedCodes.value = response.data.join('\n')
    showExportDialog.value = true
  } catch (error) {
    ElMessage.error(t('admin.inviteCodes.exportFailed'))
  }
}

const handleBatchDelete = async () => {
  if (selectedCodes.value.length === 0) {
    ElMessage.warning(t('admin.inviteCodes.selectToDelete'))
    return
  }
  
  try {
    await ElMessageBox.confirm(
      t('admin.inviteCodes.batchDeleteConfirm', { count: selectedCodes.value.length }),
      t('admin.inviteCodes.batchDeleteTitle'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    const ids = selectedCodes.value.map(item => item.id)
    await batchDeleteInviteCodes({ ids })
    ElMessage.success(t('admin.inviteCodes.batchDeleteSuccess'))
    await loadInviteCodes()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.inviteCodes.batchDeleteFailed'))
    }
  }
}

const copyExportedCodes = async () => {
  if (!exportedCodes.value) {
    ElMessage.warning(t('admin.inviteCodes.nothingToCopy'))
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(exportedCodes.value)
      ElMessage.success(t('admin.inviteCodes.copiedToClipboard'))
      return
    }
    
    // 降级方案：使用传统的 document.execCommand
    const textArea = document.createElement('textarea')
    textArea.value = exportedCodes.value
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    
    try {
      // @ts-ignore - execCommand 已废弃但作为降级方案仍需使用
      const successful = document.execCommand('copy')
      if (successful) {
        ElMessage.success(t('admin.inviteCodes.copiedToClipboard'))
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error(t('admin.inviteCodes.copyFailed'))
  }
}

const cancelCreate = () => {
  showCreateDialog.value = false
  createFormRef.value?.resetFields()
  Object.assign(createForm, {
    code: '',
    maxUses: 1,
    expiresAt: '',
    description: ''
  })
}

const submitCreate = async () => {
  try {
    await createFormRef.value.validate()
    createLoading.value = true

    const data = {
      code: createForm.code,
      count: 1,
      maxUses: createForm.maxUses,
      expiresAt: createForm.expiresAt || '',
      remark: createForm.description
    }

    await createInviteCode(data)
    ElMessage.success(t('admin.inviteCodes.createSuccess'))
    cancelCreate()
    await loadInviteCodes()
  } catch (error) {
    if (error.response?.data?.msg) {
      ElMessage.error(error.response.data.msg)
    } else {
      ElMessage.error(t('admin.inviteCodes.createFailed'))
    }
  } finally {
    createLoading.value = false
  }
}

const cancelGenerate = () => {
  showGenerateDialog.value = false
  generateFormRef.value?.resetFields()
  Object.assign(generateForm, {
    count: 1,
    maxUses: 1,
    expiresAt: '',
    description: ''
  })
}

const submitGenerate = async () => {
  try {
    await generateFormRef.value.validate()
    generateLoading.value = true

    const data = {
      count: generateForm.count,
      maxUses: generateForm.maxUses,
      expiresAt: generateForm.expiresAt || '',
      remark: generateForm.description
    }

    await generateInviteCodes(data)
    ElMessage.success(t('admin.inviteCodes.generateSuccess'))
    cancelGenerate()
    await loadInviteCodes()
  } catch (error) {
    ElMessage.error(t('admin.inviteCodes.generateFailed'))
  } finally {
    generateLoading.value = false
  }
}

const deleteCode = async (id) => {
  try {
    await ElMessageBox.confirm(
      t('admin.inviteCodes.deleteConfirm'),
      t('admin.inviteCodes.deleteTitle'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    await deleteInviteCode(id)
    ElMessage.success(t('admin.inviteCodes.deleteSuccess'))
    await loadInviteCodes()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.inviteCodes.deleteFailed'))
    }
  }
}

const handleSizeChange = (newSize) => {
  pageSize.value = newSize
  currentPage.value = 1
  loadInviteCodes()
}

const handleCurrentChange = (newPage) => {
  currentPage.value = newPage
  loadInviteCodes()
}

onMounted(() => {
  loadInviteCodes()
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

.filter-bar {
  margin-bottom: 20px;
}

.batch-actions {
  margin-bottom: 15px;
  padding: 10px;
  background-color: #f5f7fa;
  border-radius: 4px;
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

.form-tip {
  font-size: 12px;
  color: #909399;
  margin-top: 4px;
}

.export-content {
  margin: 20px 0;
}
</style>

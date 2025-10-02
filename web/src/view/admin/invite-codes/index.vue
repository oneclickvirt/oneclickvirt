<template>
  <div class="invite-codes-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>邀请码管理</span>
          <div>
            <el-button
              type="success"
              @click="showCreateDialog = true"
            >
              创建自定义邀请码
            </el-button>
            <el-button
              type="primary"
              @click="showGenerateDialog = true"
            >
              批量生成邀请码
            </el-button>
          </div>
        </div>
      </template>

      <!-- 筛选栏 -->
      <div class="filter-bar">
        <el-form :inline="true">
          <el-form-item label="使用状态">
            <el-select
              v-model="filterForm.isUsed"
              placeholder="全部"
              clearable
              style="width: 120px"
              @change="handleFilterChange"
            >
              <el-option label="全部" :value="null" />
              <el-option label="未使用" :value="false" />
              <el-option label="已使用" :value="true" />
            </el-select>
          </el-form-item>
          <el-form-item label="状态">
            <el-select
              v-model="filterForm.status"
              placeholder="全部"
              clearable
              style="width: 120px"
              @change="handleFilterChange"
            >
              <el-option label="全部" :value="0" />
              <el-option label="可用" :value="1" />
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
          导出选中 ({{ selectedCodes.length }})
        </el-button>
        <el-button
          type="danger"
          @click="handleBatchDelete"
        >
          删除选中 ({{ selectedCodes.length }})
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
          label="邀请码"
        />
        <el-table-column
          prop="maxUses"
          label="最大使用次数"
          width="120"
        >
          <template #default="scope">
            {{ scope.row.maxUses === 0 ? '无限制' : scope.row.maxUses }}
          </template>
        </el-table-column>
        <el-table-column
          prop="usedCount"
          label="已使用次数"
          width="120"
        />
        <el-table-column
          prop="status"
          label="状态"
          width="100"
        >
          <template #default="scope">
            <el-tag :type="scope.row.status === 1 ? 'success' : 'info'">
              {{ scope.row.status === 1 ? '可用' : '已失效' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="expiresAt"
          label="过期时间"
          width="160"
        >
          <template #default="scope">
            {{ scope.row.expiresAt ? new Date(scope.row.expiresAt).toLocaleString() : '永不过期' }}
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          label="创建时间"
          width="160"
        >
          <template #default="scope">
            {{ new Date(scope.row.createdAt).toLocaleString() }}
          </template>
        </el-table-column>
        <el-table-column
          label="操作"
          width="120"
        >
          <template #default="scope">
            <el-button
              size="small"
              type="danger"
              @click="deleteCode(scope.row.id)"
            >
              删除
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
      title="创建自定义邀请码" 
      width="500px"
    >
      <el-form 
        ref="createFormRef" 
        :model="createForm" 
        :rules="createRules" 
        label-width="120px"
      >
        <el-form-item
          label="邀请码"
          prop="code"
        >
          <el-input 
            v-model="createForm.code" 
            placeholder="请输入自定义邀请码"
            maxlength="50"
            show-word-limit
          />
          <div class="form-tip">
            邀请码只能包含数字和英文大写字母
          </div>
        </el-form-item>
        <el-form-item
          label="最大使用次数"
          prop="maxUses"
        >
          <el-input-number
            v-model="createForm.maxUses"
            :min="0"
          />
          <div class="form-tip">
            设置为0表示无限制使用
          </div>
        </el-form-item>
        <el-form-item
          label="过期时间"
          prop="expiresAt"
        >
          <el-date-picker
            v-model="createForm.expiresAt"
            type="datetime"
            placeholder="选择过期时间"
            format="YYYY-MM-DD HH:mm:ss"
            value-format="YYYY-MM-DD HH:mm:ss"
            style="width: 100%"
          />
          <div class="form-tip">
            不设置表示永不过期
          </div>
        </el-form-item>
        <el-form-item
          label="描述"
          prop="description"
        >
          <el-input 
            v-model="createForm.description" 
            type="textarea" 
            :rows="3"
            placeholder="邀请码用途描述"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelCreate">取消</el-button>
          <el-button
            type="primary"
            :loading="createLoading"
            @click="submitCreate"
          >创建</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 生成邀请码对话框 -->
    <el-dialog 
      v-model="showGenerateDialog" 
      title="批量生成邀请码" 
      width="500px"
    >
      <el-form 
        ref="generateFormRef" 
        :model="generateForm" 
        :rules="generateRules" 
        label-width="120px"
      >
        <el-form-item
          label="生成数量"
          prop="count"
        >
          <el-input-number
            v-model="generateForm.count"
            :min="1"
            :max="100"
          />
        </el-form-item>
        <el-form-item
          label="最大使用次数"
          prop="maxUses"
        >
          <el-input-number
            v-model="generateForm.maxUses"
            :min="0"
          />
          <div class="form-tip">
            设置为0表示无限制使用
          </div>
        </el-form-item>
        <el-form-item
          label="过期时间"
          prop="expiresAt"
        >
          <el-date-picker
            v-model="generateForm.expiresAt"
            type="datetime"
            placeholder="选择过期时间"
            format="YYYY-MM-DD HH:mm:ss"
            value-format="YYYY-MM-DD HH:mm:ss"
            style="width: 100%"
          />
          <div class="form-tip">
            不设置表示永不过期
          </div>
        </el-form-item>
        <el-form-item
          label="描述"
          prop="description"
        >
          <el-input 
            v-model="generateForm.description" 
            type="textarea" 
            :rows="3"
            placeholder="邀请码用途描述"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelGenerate">取消</el-button>
          <el-button
            type="primary"
            :loading="generateLoading"
            @click="submitGenerate"
          >生成</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 导出邀请码对话框 -->
    <el-dialog
      v-model="showExportDialog"
      title="导出邀请码"
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
          <el-button @click="showExportDialog = false">关闭</el-button>
          <el-button
            type="primary"
            @click="copyExportedCodes"
          >复制</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getInviteCodes, createInviteCode, generateInviteCodes, deleteInviteCode, batchDeleteInviteCodes, exportInviteCodes } from '@/api/admin'

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
    { required: true, message: '请输入邀请码', trigger: 'blur' },
    { min: 3, max: 50, message: '邀请码长度为3-50个字符', trigger: 'blur' },
    { pattern: /^[0-9A-Z]+$/, message: '邀请码只能包含数字和英文大写字母', trigger: 'blur' }
  ],
  maxUses: [
    { required: true, message: '请输入最大使用次数', trigger: 'blur' }
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
    { required: true, message: '请输入生成数量', trigger: 'blur' }
  ],
  maxUses: [
    { required: true, message: '请输入最大使用次数', trigger: 'blur' }
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
    ElMessage.error('加载邀请码列表失败')
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
    ElMessage.warning('请选择要导出的邀请码')
    return
  }
  
  try {
    const ids = selectedCodes.value.map(item => item.id)
    const response = await exportInviteCodes({ ids })
    exportedCodes.value = response.data.join('\n')
    showExportDialog.value = true
  } catch (error) {
    ElMessage.error('导出邀请码失败')
  }
}

const handleBatchDelete = async () => {
  if (selectedCodes.value.length === 0) {
    ElMessage.warning('请选择要删除的邀请码')
    return
  }
  
  try {
    await ElMessageBox.confirm(
      `确定删除选中的 ${selectedCodes.value.length} 个邀请码吗？此操作不可恢复。`,
      '批量删除邀请码',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    const ids = selectedCodes.value.map(item => item.id)
    await batchDeleteInviteCodes({ ids })
    ElMessage.success('批量删除成功')
    await loadInviteCodes()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量删除失败')
    }
  }
}

const copyExportedCodes = async () => {
  if (!exportedCodes.value) {
    ElMessage.warning('没有可复制的内容')
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(exportedCodes.value)
      ElMessage.success('已复制到剪贴板')
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
        ElMessage.success('已复制到剪贴板')
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error('复制失败，请手动复制')
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
    ElMessage.success('自定义邀请码创建成功')
    cancelCreate()
    await loadInviteCodes()
  } catch (error) {
    if (error.response?.data?.msg) {
      ElMessage.error(error.response.data.msg)
    } else {
      ElMessage.error('邀请码创建失败')
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
    ElMessage.success('邀请码生成成功')
    cancelGenerate()
    await loadInviteCodes()
  } catch (error) {
    ElMessage.error('邀请码生成失败')
  } finally {
    generateLoading.value = false
  }
}

const deleteCode = async (id) => {
  try {
    await ElMessageBox.confirm(
      '确定删除该邀请码吗？此操作不可恢复。',
      '删除邀请码',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    await deleteInviteCode(id)
    ElMessage.success('删除成功')
    await loadInviteCodes()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('删除失败')
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
.invite-codes-container {
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
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

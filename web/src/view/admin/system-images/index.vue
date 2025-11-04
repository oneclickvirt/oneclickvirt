<template>
  <div class="system-images-container">
    <el-card class="box-card">
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.systemImages.title') }}</span>
          <el-button
            type="primary"
            @click="handleCreate"
          >
            <el-icon><Plus /></el-icon>
            {{ $t('admin.systemImages.addImage') }}
          </el-button>
        </div>
      </template>

      <!-- 搜索过滤 -->
      <div class="filter-container">
        <el-row :gutter="20">
          <el-col :span="6">
            <el-input
              v-model="searchForm.search"
              :placeholder="$t('admin.systemImages.searchPlaceholder')"
              clearable
              @clear="handleSearch"
              @keyup.enter="handleSearch"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.providerType"
              :placeholder="$t('admin.systemImages.providerType')"
              clearable
              @change="handleSearch"
              style="width: 100%;"
            >
              <el-option
                label="ProxmoxVE"
                value="proxmox"
              />
              <el-option
                label="LXD"
                value="lxd"
              />
              <el-option
                label="Incus"
                value="incus"
              />
              <el-option
                label="Docker"
                value="docker"
              />
            </el-select>
          </el-col>
          <el-col :span="3">
            <el-select
              v-model="searchForm.instanceType"
              :placeholder="$t('admin.systemImages.instanceType')"
              clearable
              @change="handleSearch"
              style="width: 100%;"
            >
              <el-option
                :label="$t('admin.systemImages.vm')"
                value="vm"
              />
              <el-option
                :label="$t('admin.systemImages.container')"
                value="container"
              />
            </el-select>
          </el-col>
          <el-col :span="3">
            <el-select
              v-model="searchForm.architecture"
              :placeholder="$t('admin.systemImages.architecture')"
              clearable
              @change="handleSearch"
              style="width: 100%;"
            >
              <el-option
                label="amd64"
                value="amd64"
              />
              <el-option
                label="arm64"
                value="arm64"
              />
              <el-option
                label="s390x"
                value="s390x"
              />
            </el-select>
          </el-col>
          <el-col :span="3">
            <el-select
              v-model="searchForm.osType"
              :placeholder="$t('admin.systemImages.osType')"
              clearable
              style="width: 100%;"
              @change="handleSearch"
            >
              <el-option-group
                v-for="(osList, category) in groupedOperatingSystems"
                :key="category"
                :label="category"
              >
                <el-option
                  v-for="os in osList"
                  :key="os.name"
                  :label="os.displayName"
                  :value="os.name"
                />
              </el-option-group>
            </el-select>
          </el-col>
          <el-col :span="3">
            <el-select
              v-model="searchForm.status"
              :placeholder="$t('common.status')"
              clearable
              @change="handleSearch"
              style="width: 100%;"
            >
              <el-option
                :label="$t('admin.systemImages.active')"
                value="active"
              />
              <el-option
                :label="$t('admin.systemImages.inactive')"
                value="inactive"
              />
            </el-select>
          </el-col>
          <el-col :span="4">
            <el-button
              type="primary"
              @click="handleSearch"
            >
              {{ $t('common.search') }}
            </el-button>
            <el-button @click="handleReset">
              {{ $t('common.reset') }}
            </el-button>
          </el-col>
        </el-row>
      </div>

      <!-- 批量操作 -->
      <div
        v-if="selectedRows.length > 0"
        class="batch-actions"
      >
        <el-alert
          :title="$t('admin.systemImages.selectedCount', { count: selectedRows.length })"
          type="info"
          show-icon
          :closable="false"
        >
          <template #default>
            <el-button
              type="success"
              size="small"
              @click="handleBatchStatus('active')"
            >
              {{ $t('admin.systemImages.batchActivate') }}
            </el-button>
            <el-button
              type="warning"
              size="small"
              @click="handleBatchStatus('inactive')"
            >
              {{ $t('admin.systemImages.batchDisable') }}
            </el-button>
            <el-button
              type="danger"
              size="small"
              @click="handleBatchDelete"
            >
              {{ $t('admin.systemImages.batchDelete') }}
            </el-button>
          </template>
        </el-alert>
      </div>

      <!-- 数据表格 -->
      <el-table
        v-loading="loading"
        :data="tableData"
        class="system-images-table"
        :row-style="{ height: '60px' }"
        :cell-style="{ padding: '12px 0' }"
        :header-cell-style="{ background: '#f5f7fa', padding: '14px 0', fontWeight: '600' }"
        stripe
        border
        @selection-change="handleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
          align="center"
        />
        <el-table-column
          prop="name"
          :label="$t('admin.systemImages.imageName')"
          min-width="140"
          show-overflow-tooltip
        />
        <el-table-column
          :label="$t('admin.systemImages.providerType')"
          width="130"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="getProviderTypeColor(scope.row.providerType)">
              {{ getProviderTypeName(scope.row.providerType) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.systemImages.instanceType')"
          width="110"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="scope.row.instanceType === 'vm' ? 'primary' : 'success'">
              {{ scope.row.instanceType === 'vm' ? $t('admin.systemImages.vm') : $t('admin.systemImages.container') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="architecture"
          :label="$t('admin.systemImages.architecture')"
          width="110"
          align="center"
          show-overflow-tooltip
        />
        <el-table-column
          :label="$t('admin.systemImages.osType')"
          width="150"
          show-overflow-tooltip
        >
          <template #default="scope">
            {{ getDisplayName(scope.row.osType) || scope.row.osType || '-' }}
          </template>
        </el-table-column>
        <el-table-column
          prop="osVersion"
          :label="$t('admin.systemImages.version')"
          width="120"
          show-overflow-tooltip
        />
        <el-table-column
          label="URL"
          min-width="200"
          show-overflow-tooltip
        >
          <template #default="scope">
            <span class="url-text">{{ scope.row.url }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.systemImages.size')"
          width="100"
          align="center"
        >
          <template #default="scope">
            {{ formatFileSize(scope.row.size) }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.status')"
          width="100"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="scope.row.status === 'active' ? 'success' : 'danger'">
              {{ scope.row.status === 'active' ? $t('admin.systemImages.active') : $t('admin.systemImages.inactive') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.createTime')"
          width="180"
          align="center"
        >
          <template #default="scope">
            {{ formatDateTime(scope.row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="240"
          fixed="right"
          align="center"
        >
          <template #default="scope">
            <div class="action-buttons">
              <el-button
                type="primary"
                size="small"
                @click="handleEdit(scope.row)"
              >
                {{ $t('common.edit') }}
              </el-button>
              <el-button
                :type="scope.row.status === 'active' ? 'warning' : 'success'"
                size="small"
                @click="handleToggleStatus(scope.row)"
              >
                {{ scope.row.status === 'active' ? $t('common.disable') : $t('admin.systemImages.activate') }}
              </el-button>
              <el-button
                type="danger"
                size="small"
                @click="handleDelete(scope.row)"
              >
                {{ $t('common.delete') }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-container">
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="pagination.total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>

    <!-- 创建/编辑对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="800px"
      :before-close="handleDialogClose"
    >
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-width="120px"
      >
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.imageName')"
              prop="name"
            >
              <el-input
                v-model="form.name"
                :placeholder="$t('admin.systemImages.imageNamePlaceholder')"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.providerType')"
              prop="providerType"
            >
              <el-select
                v-model="form.providerType"
                :placeholder="$t('admin.systemImages.selectProviderType')"
                @change="handleProviderTypeChange"
              >
                <el-option
                  label="ProxmoxVE"
                  value="proxmox"
                />
                <el-option
                  label="LXD"
                  value="lxd"
                />
                <el-option
                  label="Incus"
                  value="incus"
                />
                <el-option
                  label="Docker"
                  value="docker"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.instanceType')"
              prop="instanceType"
            >
              <el-select
                v-model="form.instanceType"
                :placeholder="$t('admin.systemImages.selectInstanceType')"
                @change="handleInstanceTypeChange"
              >
                <el-option
                  :label="$t('admin.systemImages.vm')"
                  value="vm"
                />
                <el-option
                  :label="$t('admin.systemImages.container')"
                  value="container"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.architecture')"
              prop="architecture"
            >
              <el-select
                v-model="form.architecture"
                :placeholder="$t('admin.systemImages.selectArchitecture')"
              >
                <el-option
                  label="amd64"
                  value="amd64"
                />
                <el-option
                  label="arm64"
                  value="arm64"
                />
                <el-option
                  label="s390x"
                  value="s390x"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item
          :label="$t('admin.systemImages.imageUrl')"
          prop="url"
        >
          <el-input
            v-model="form.url"
            :placeholder="$t('admin.systemImages.imageUrlPlaceholder')"
          />
          <div class="form-hint">
            <template v-if="getUrlHint()">
              {{ getUrlHint() }}
            </template>
          </div>
        </el-form-item>
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.osType')"
              prop="osType"
            >
              <el-select 
                v-model="form.osType" 
                :placeholder="$t('admin.systemImages.selectOsType')"
                filterable
                @change="handleOsTypeChange"
              >
                <el-option-group
                  v-for="(osList, category) in groupedOperatingSystems"
                  :key="category"
                  :label="category"
                >
                  <el-option
                    v-for="os in osList"
                    :key="os.name"
                    :label="os.displayName"
                    :value="os.name"
                  />
                </el-option-group>
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.osVersion')"
              prop="osVersion"
            >
              <el-select 
                v-model="form.osVersion" 
                :placeholder="$t('admin.systemImages.selectOsVersion')"
                filterable
                allow-create
                default-first-option
              >
                <el-option
                  v-for="version in availableVersions"
                  :key="version"
                  :label="version"
                  :value="version"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item :label="$t('admin.systemImages.fileSize')">
              <el-input
                v-model.number="form.size"
                type="number"
                :placeholder="$t('admin.systemImages.optional')"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item :label="$t('admin.systemImages.checksum')">
              <el-input
                v-model="form.checksum"
                :placeholder="$t('admin.systemImages.checksumPlaceholder')"
              />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.minMemoryMB')"
              prop="minMemoryMB"
            >
              <el-input
                v-model.number="form.minMemoryMB"
                type="number"
                :placeholder="$t('admin.systemImages.minMemoryPlaceholder')"
              />
              <div class="form-hint">
                {{ $t('admin.systemImages.minMemoryHint') }}
              </div>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.systemImages.minDiskMB')"
              prop="minDiskMB"
            >
              <el-input
                v-model.number="form.minDiskMB"
                type="number"
                :placeholder="$t('admin.systemImages.minDiskPlaceholder')"
              />
              <div class="form-hint">
                {{ $t('admin.systemImages.minDiskHint') }}
              </div>
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item :label="$t('admin.systemImages.useCdn')">
          <el-switch
            v-model="form.useCdn"
            :active-text="$t('admin.systemImages.useCdnActive')"
            :inactive-text="$t('admin.systemImages.useCdnInactive')"
          />
          <div class="form-hint">
            {{ $t('admin.systemImages.useCdnHint') }}
          </div>
        </el-form-item>
        <el-form-item :label="$t('admin.systemImages.tags')">
          <el-input
            v-model="form.tags"
            :placeholder="$t('admin.systemImages.tagsPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="$t('common.description')">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="3"
            :placeholder="$t('admin.systemImages.descriptionPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="handleDialogClose">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="submitting"
            @click="handleSubmit"
          >
            {{ $t('common.confirm') }}
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Search } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { systemImageApi } from '@/api/admin'
import { 
  getOperatingSystemsByCategory, 
  getCommonVersions,
  getDisplayName 
} from '@/utils/operating-systems'

const { t } = useI18n()

// 响应式数据
const loading = ref(false)
const submitting = ref(false)
const dialogVisible = ref(false)
const selectedRows = ref([])
const tableData = ref([])

// 搜索表单
const searchForm = reactive({
  search: '',
  providerType: '',
  instanceType: '',
  architecture: '',
  osType: '',
  status: ''
})

// 分页
const pagination = reactive({
  page: 1,
  pageSize: 10,
  total: 0
})

// 表单数据
const form = reactive({
  name: '',
  providerType: '',
  instanceType: '',
  architecture: '',
  url: '',
  checksum: '',
  size: null,
  description: '',
  osType: '',
  osVersion: '',
  tags: '',
  minMemoryMB: null,
  minDiskMB: null,
  useCdn: true
})

// 表单引用
const formRef = ref()

// 编辑模式
const isEdit = ref(false)
const editId = ref(null)

// 操作系统数据
const groupedOperatingSystems = ref(getOperatingSystemsByCategory())
const availableVersions = ref([])

// 计算属性
const dialogTitle = computed(() => isEdit.value ? t('admin.systemImages.editImage') : t('admin.systemImages.addImage'))

// 表单验证规则
const rules = {
  name: [
    { required: true, message: t('admin.systemImages.imageNameRequired'), trigger: 'blur' }
  ],
  providerType: [
    { required: true, message: t('admin.systemImages.providerTypeRequired'), trigger: 'change' }
  ],
  instanceType: [
    { required: true, message: t('admin.systemImages.instanceTypeRequired'), trigger: 'change' }
  ],
  architecture: [
    { required: true, message: t('admin.systemImages.architectureRequired'), trigger: 'change' }
  ],
  url: [
    { required: true, message: t('admin.systemImages.urlRequired'), trigger: 'blur' },
    { type: 'url', message: t('admin.systemImages.urlInvalid'), trigger: 'blur' }
  ],
  minMemoryMB: [
    { required: true, message: t('admin.systemImages.minMemoryRequired'), trigger: 'blur' },
    { type: 'number', min: 1, message: t('admin.systemImages.minMemoryInvalid'), trigger: 'blur' }
  ],
  minDiskMB: [
    { required: true, message: t('admin.systemImages.minDiskRequired'), trigger: 'blur' },
    { type: 'number', min: 1, message: t('admin.systemImages.minDiskInvalid'), trigger: 'blur' }
  ]
}

// 获取数据
const fetchData = async () => {
  loading.value = true
  try {
    const params = {
      page: pagination.page,
      pageSize: pagination.pageSize,
      ...searchForm
    }
    
    const response = await systemImageApi.getList(params)
    if (response.code === 0 || response.code === 200) {
      tableData.value = response.data.list || []
      pagination.total = response.data.total || 0
    }
  } catch (error) {
    ElMessage.error(t('admin.systemImages.loadFailed') + ': ' + error.message)
  } finally {
    loading.value = false
  }
}

// 搜索
const handleSearch = () => {
  pagination.page = 1
  fetchData()
}

// 重置搜索
const handleReset = () => {
  Object.assign(searchForm, {
    search: '',
    providerType: '',
    instanceType: '',
    architecture: '',
    osType: '',
    status: ''
  })
  handleSearch()
}

// 选择变化
const handleSelectionChange = (selection) => {
  selectedRows.value = selection
}

// 创建
const handleCreate = () => {
  isEdit.value = false
  editId.value = null
  resetForm()
  dialogVisible.value = true
}

// 编辑
const handleEdit = (row) => {
  isEdit.value = true
  editId.value = row.id
  Object.assign(form, {
    name: row.name,
    providerType: row.providerType,
    instanceType: row.instanceType,
    architecture: row.architecture,
    url: row.url,
    checksum: row.checksum || '',
    size: row.size || null,
    description: row.description || '',
    osType: row.osType || '',
    osVersion: row.osVersion || '',
    tags: row.tags || '',
    minMemoryMB: row.minMemoryMB || null,
    minDiskMB: row.minDiskMB || null,
    useCdn: row.useCdn !== undefined ? row.useCdn : true
  })
  
  // 设置可用版本
  if (form.osType) {
    availableVersions.value = getCommonVersions(form.osType)
  }
  
  dialogVisible.value = true
}

// 提交表单
const handleSubmit = async () => {
  if (!formRef.value) return
  
  try {
    await formRef.value.validate()
    submitting.value = true
    
    const data = { ...form }
    
    if (isEdit.value) {
      await systemImageApi.update(editId.value, data)
      ElMessage.success(t('admin.systemImages.updateSuccess'))
    } else {
      await systemImageApi.create(data)
      ElMessage.success(t('admin.systemImages.createSuccess'))
    }
    
    dialogVisible.value = false
    fetchData()
  } catch (error) {
    if (error.message) {
      ElMessage.error(error.message)
    }
  } finally {
    submitting.value = false
  }
}

// 删除
const handleDelete = async (row) => {
  try {
    await ElMessageBox.confirm(
      t('admin.systemImages.deleteConfirm', { name: row.name }),
      t('admin.systemImages.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    
    await systemImageApi.delete(row.id)
    ElMessage.success(t('admin.systemImages.deleteSuccess'))
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.systemImages.deleteFailed') + ': ' + error.message)
    }
  }
}

// 切换状态
const handleToggleStatus = async (row) => {
  const newStatus = row.status === 'active' ? 'inactive' : 'active'
  const action = newStatus === 'active' ? t('admin.systemImages.activate') : t('common.disable')
  
  try {
    await ElMessageBox.confirm(
      t('admin.systemImages.toggleStatusConfirm', { action, name: row.name }),
      t('admin.systemImages.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    
    await systemImageApi.update(row.id, { status: newStatus })
    ElMessage.success(t('admin.systemImages.toggleStatusSuccess', { action }))
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.systemImages.toggleStatusFailed', { action }) + ': ' + error.message)
    }
  }
}

// 批量删除
const handleBatchDelete = async () => {
  try {
    await ElMessageBox.confirm(
      t('admin.systemImages.batchDeleteConfirm', { count: selectedRows.value.length }),
      t('admin.systemImages.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    
    const ids = selectedRows.value.map(row => row.id)
    await systemImageApi.batchDelete({ ids })
    ElMessage.success(t('admin.systemImages.batchDeleteSuccess'))
    selectedRows.value = []
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.systemImages.batchDeleteFailed') + ': ' + error.message)
    }
  }
}

// 批量状态
const handleBatchStatus = async (status) => {
  const action = status === 'active' ? t('admin.systemImages.activate') : t('common.disable')
  
  try {
    await ElMessageBox.confirm(
      t('admin.systemImages.batchStatusConfirm', { action, count: selectedRows.value.length }),
      t('admin.systemImages.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    
    const ids = selectedRows.value.map(row => row.id)
    await systemImageApi.batchUpdateStatus({ ids, status })
    ElMessage.success(t('admin.systemImages.batchStatusSuccess', { action }))
    selectedRows.value = []
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.systemImages.batchStatusFailed', { action }) + ': ' + error.message)
    }
  }
}

// 分页变化
const handleSizeChange = (size) => {
  pagination.pageSize = size
  pagination.page = 1
  fetchData()
}

const handleCurrentChange = (page) => {
  pagination.page = page
  fetchData()
}

// 对话框关闭
const handleDialogClose = () => {
  dialogVisible.value = false
  resetForm()
}

// 重置表单
const resetForm = () => {
  if (formRef.value) {
    formRef.value.resetFields()
  }
  Object.assign(form, {
    name: '',
    providerType: '',
    instanceType: '',
    architecture: '',
    url: '',
    checksum: '',
    size: null,
    description: '',
    osType: '',
    osVersion: '',
    tags: '',
    minMemoryMB: null,
    minDiskMB: null,
    useCdn: true
  })
}

// Provider类型变化
const handleProviderTypeChange = () => {
  // 根据Provider类型清除不兼容的实例类型
  if (form.providerType === 'docker' && form.instanceType === 'vm') {
    form.instanceType = ''
  }
}

// 实例类型变化
const handleInstanceTypeChange = () => {
  // 可以在这里添加逻辑
}

// 操作系统类型变化
const handleOsTypeChange = () => {
  // 更新可用版本列表
  if (form.osType) {
    availableVersions.value = getCommonVersions(form.osType)
    // 清空之前选择的版本
    form.osVersion = ''
  } else {
    availableVersions.value = []
  }
}

// 获取URL提示
const getUrlHint = () => {
  if (!form.providerType || !form.instanceType) return ''
  
  if (form.providerType === 'proxmox' && form.instanceType === 'vm') {
    return 'ProxmoxVE虚拟机镜像必须是 .qcow2 文件'
  } else if ((form.providerType === 'lxd' || form.providerType === 'incus')) {
    return 'LXD/Incus镜像必须是 .zip 文件'
  } else if (form.providerType === 'docker' && form.instanceType === 'container') {
    return 'Docker容器镜像必须是 .tar.gz 文件'
  }
  return ''
}

// 获取Provider类型名称
const getProviderTypeName = (type) => {
  const names = {
    proxmox: 'ProxmoxVE',
    lxd: 'LXD',
    incus: 'Incus',
    docker: 'Docker'
  }
  return names[type] || type
}

// 获取Provider类型颜色
const getProviderTypeColor = (type) => {
  const colors = {
    proxmox: 'primary',
    lxd: 'success',
    incus: 'warning',
    docker: 'info'
  }
  return colors[type] || ''
}

// 截断URL显示
const truncateUrl = (url) => {
  if (!url) return ''
  return url.length > 50 ? url.substring(0, 50) + '...' : url
}

// 格式化文件大小
const formatFileSize = (bytes) => {
  if (!bytes || bytes === 0) return '-'
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return Math.round(bytes / Math.pow(1024, i) * 100) / 100 + ' ' + sizes[i]
}

// 格式化时间
const formatDateTime = (dateTime) => {
  if (!dateTime) return '-'
  return new Date(dateTime).toLocaleString('zh-CN')
}

// 页面挂载
onMounted(() => {
  fetchData()
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

.system-images-table {
  width: 100%;
  
  .action-buttons {
    display: flex;
    gap: 10px;
    justify-content: center;
    align-items: center;
    flex-wrap: wrap;
    padding: 4px 0;
    
    .el-button {
      margin: 0 !important;
    }
  }
  
  :deep(.el-table__cell) {
    .cell {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
}

.filter-container {
  margin-bottom: 20px;
}

.batch-actions {
  margin-bottom: 16px;
}

.pagination-container {
  margin-top: 20px;
  text-align: center;
}

.url-text {
  cursor: pointer;
  color: #409eff;
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.is-default {
  color: #f56c6c;
}

.form-hint {
  font-size: 12px;
  color: #909399;
  margin-top: 4px;
}

.dialog-footer {
  text-align: right;
}

:deep(.el-table) {
  margin-bottom: 0;
}
</style>

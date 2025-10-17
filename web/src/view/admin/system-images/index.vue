<template>
  <div class="system-images-container">
    <el-card class="box-card">
      <template #header>
        <div class="card-header">
          <span>系统管理</span>
          <el-button
            type="primary"
            @click="handleCreate"
          >
            <el-icon><Plus /></el-icon>
            添加镜像
          </el-button>
        </div>
      </template>

      <!-- 搜索过滤 -->
      <div class="filter-container">
        <el-row :gutter="20">
          <el-col :span="6">
            <el-input
              v-model="searchForm.search"
              placeholder="请输入镜像名称、描述或操作系统"
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
              placeholder="Provider类型"
              clearable
              @change="handleSearch"
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
              placeholder="实例类型"
              clearable
              @change="handleSearch"
            >
              <el-option
                label="虚拟机"
                value="vm"
              />
              <el-option
                label="容器"
                value="container"
              />
            </el-select>
          </el-col>
          <el-col :span="3">
            <el-select
              v-model="searchForm.architecture"
              placeholder="架构"
              clearable
              @change="handleSearch"
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
              placeholder="操作系统"
              clearable
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
              placeholder="状态"
              clearable
              @change="handleSearch"
            >
              <el-option
                label="激活"
                value="active"
              />
              <el-option
                label="禁用"
                value="inactive"
              />
            </el-select>
          </el-col>
          <el-col :span="4">
            <el-button
              type="primary"
              @click="handleSearch"
            >
              搜索
            </el-button>
            <el-button @click="handleReset">
              重置
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
          :title="`已选择 ${selectedRows.length} 项`"
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
              批量激活
            </el-button>
            <el-button
              type="warning"
              size="small"
              @click="handleBatchStatus('inactive')"
            >
              批量禁用
            </el-button>
            <el-button
              type="danger"
              size="small"
              @click="handleBatchDelete"
            >
              批量删除
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
          label="镜像名称"
          min-width="140"
          show-overflow-tooltip
        />
        <el-table-column
          label="Provider类型"
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
          label="实例类型"
          width="110"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="scope.row.instanceType === 'vm' ? 'primary' : 'success'">
              {{ scope.row.instanceType === 'vm' ? '虚拟机' : '容器' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="architecture"
          label="架构"
          width="100"
          align="center"
          show-overflow-tooltip
        />
        <el-table-column
          label="操作系统"
          width="140"
          show-overflow-tooltip
        >
          <template #default="scope">
            {{ getDisplayName(scope.row.osType) || scope.row.osType || '-' }}
          </template>
        </el-table-column>
        <el-table-column
          prop="osVersion"
          label="版本"
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
          label="大小"
          width="100"
          align="center"
        >
          <template #default="scope">
            {{ formatFileSize(scope.row.size) }}
          </template>
        </el-table-column>
        <el-table-column
          label="状态"
          width="100"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="scope.row.status === 'active' ? 'success' : 'danger'">
              {{ scope.row.status === 'active' ? '激活' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          label="创建时间"
          width="180"
          align="center"
        >
          <template #default="scope">
            {{ formatDateTime(scope.row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          label="操作"
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
                编辑
              </el-button>
              <el-button
                :type="scope.row.status === 'active' ? 'warning' : 'success'"
                size="small"
                @click="handleToggleStatus(scope.row)"
              >
                {{ scope.row.status === 'active' ? '禁用' : '激活' }}
              </el-button>
              <el-button
                type="danger"
                size="small"
                @click="handleDelete(scope.row)"
              >
                删除
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
              label="镜像名称"
              prop="name"
            >
              <el-input
                v-model="form.name"
                placeholder="请输入镜像名称"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="Provider类型"
              prop="providerType"
            >
              <el-select
                v-model="form.providerType"
                placeholder="请选择Provider类型"
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
              label="实例类型"
              prop="instanceType"
            >
              <el-select
                v-model="form.instanceType"
                placeholder="请选择实例类型"
                @change="handleInstanceTypeChange"
              >
                <el-option
                  label="虚拟机"
                  value="vm"
                />
                <el-option
                  label="容器"
                  value="container"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="架构"
              prop="architecture"
            >
              <el-select
                v-model="form.architecture"
                placeholder="请选择架构"
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
          label="镜像地址"
          prop="url"
        >
          <el-input
            v-model="form.url"
            placeholder="请输入镜像下载地址"
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
              label="操作系统"
              prop="osType"
            >
              <el-select 
                v-model="form.osType" 
                placeholder="请选择操作系统"
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
              label="操作系统版本"
              prop="osVersion"
            >
              <el-select 
                v-model="form.osVersion" 
                placeholder="请选择或输入版本"
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
            <el-form-item label="文件大小(字节)">
              <el-input
                v-model.number="form.size"
                type="number"
                placeholder="可选"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="校验和">
              <el-input
                v-model="form.checksum"
                placeholder="文件校验和(可选)"
              />
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item label="标签">
          <el-input
            v-model="form.tags"
            placeholder="多个标签用逗号分隔"
          />
        </el-form-item>
        <el-form-item label="描述">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="3"
            placeholder="请输入镜像描述"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="handleDialogClose">取消</el-button>
          <el-button
            type="primary"
            :loading="submitting"
            @click="handleSubmit"
          >
            确认
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
import { systemImageApi } from '@/api/admin'
import { 
  getOperatingSystemsByCategory, 
  getCommonVersions,
  getDisplayName 
} from '@/utils/operating-systems'

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
  tags: ''
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
const dialogTitle = computed(() => isEdit.value ? '编辑镜像' : '添加镜像')

// 表单验证规则
const rules = {
  name: [
    { required: true, message: '请输入镜像名称', trigger: 'blur' }
  ],
  providerType: [
    { required: true, message: '请选择Provider类型', trigger: 'change' }
  ],
  instanceType: [
    { required: true, message: '请选择实例类型', trigger: 'change' }
  ],
  architecture: [
    { required: true, message: '请选择架构', trigger: 'change' }
  ],
  url: [
    { required: true, message: '请输入镜像地址', trigger: 'blur' },
    { type: 'url', message: '请输入有效的URL地址', trigger: 'blur' }
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
    ElMessage.error('获取数据失败: ' + error.message)
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
    tags: row.tags || ''
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
      ElMessage.success('更新成功')
    } else {
      await systemImageApi.create(data)
      ElMessage.success('创建成功')
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
      `确认删除镜像 "${row.name}" 吗？`,
      '警告',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    await systemImageApi.delete(row.id)
    ElMessage.success('删除成功')
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('删除失败: ' + error.message)
    }
  }
}

// 切换状态
const handleToggleStatus = async (row) => {
  const newStatus = row.status === 'active' ? 'inactive' : 'active'
  const action = newStatus === 'active' ? '激活' : '禁用'
  
  try {
    await ElMessageBox.confirm(
      `确认${action}镜像 "${row.name}" 吗？`,
      '确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    await systemImageApi.update(row.id, { status: newStatus })
    ElMessage.success(`${action}成功`)
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(`${action}失败: ` + error.message)
    }
  }
}

// 批量删除
const handleBatchDelete = async () => {
  try {
    await ElMessageBox.confirm(
      `确认删除选中的 ${selectedRows.value.length} 个镜像吗？`,
      '警告',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    const ids = selectedRows.value.map(row => row.id)
    await systemImageApi.batchDelete({ ids })
    ElMessage.success('批量删除成功')
    selectedRows.value = []
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量删除失败: ' + error.message)
    }
  }
}

// 批量状态
const handleBatchStatus = async (status) => {
  const action = status === 'active' ? '激活' : '禁用'
  
  try {
    await ElMessageBox.confirm(
      `确认${action}选中的 ${selectedRows.value.length} 个镜像吗？`,
      '确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    const ids = selectedRows.value.map(row => row.id)
    await systemImageApi.batchUpdateStatus({ ids, status })
    ElMessage.success(`批量${action}成功`)
    selectedRows.value = []
    fetchData()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(`批量${action}失败: ` + error.message)
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
    tags: ''
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
.system-images-container {
  padding: 24px;
  
  .box-card {
    :deep(.el-card__header) {
      padding: 20px 24px;
      border-bottom: 1px solid #ebeef5;
    }
    
    :deep(.el-card__body) {
      padding: 24px;
    }
  }
}

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

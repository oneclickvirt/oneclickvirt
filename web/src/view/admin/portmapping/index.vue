<template>
  <div>
    <el-card class="box-card">
      <template #header>
        <div class="card-header">
          <span>端口映射管理</span>
          <el-button
            type="primary"
            @click="showBatchDialog = true"
          >
            批量操作
          </el-button>
        </div>
      </template>
      
      <!-- 搜索和筛选 -->
      <div class="search-bar">
        <el-row :gutter="20">
          <el-col :span="6">
            <el-input 
              v-model="searchForm.keyword" 
              placeholder="搜索实例名称"
              clearable
              @keyup.enter="searchPortMappings"
            >
              <template #append>
                <el-button
                  icon="Search"
                  @click="searchPortMappings"
                />
              </template>
            </el-input>
          </el-col>
          <el-col :span="6">
            <el-select
              v-model="searchForm.providerId"
              placeholder="选择Provider"
              clearable
            >
              <el-option
                v-for="provider in providers"
                :key="provider.id"
                :label="provider.name"
                :value="provider.id"
              />
            </el-select>
          </el-col>
          <el-col :span="6">
            <el-select
              v-model="searchForm.status"
              placeholder="状态"
              clearable
            >
              <el-option
                label="活跃"
                value="active"
              />
              <el-option
                label="未使用"
                value="inactive"
              />
            </el-select>
          </el-col>
          <el-col :span="6">
            <el-button
              type="primary"
              @click="searchPortMappings"
            >
              搜索
            </el-button>
            <el-button @click="resetSearch">
              重置
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
        />
        <el-table-column
          prop="id"
          label="ID"
          width="80"
        />
        <el-table-column
          prop="instanceName"
          label="实例名称"
          width="150"
        />
        <el-table-column
          prop="providerName"
          label="Provider"
          width="120"
        />
        <el-table-column
          prop="publicIP"
          label="公网IP"
          width="120"
        />
        <el-table-column
          prop="hostPort"
          label="公网端口"
          width="100"
        />
        <el-table-column
          prop="guestPort"
          label="内部端口"
          width="100"
        />
        <el-table-column
          prop="protocol"
          label="协议"
          width="80"
        />
        <el-table-column
          prop="isIPv6"
          label="IPv6"
          width="80"
        >
          <template #default="{ row }">
            <el-tag :type="row.isIPv6 ? 'success' : 'info'">
              {{ row.isIPv6 ? '是' : '否' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          label="状态"
          width="100"
        >
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'">
              {{ row.status === 'active' ? '活跃' : '未使用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          label="创建时间"
          width="150"
        >
          <template #default="{ row }">
            {{ formatTime(row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          label="操作"
          width="200"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button
              type="primary"
              size="small"
              @click="editPortMapping(row)"
            >
              编辑
            </el-button>
            <el-button
              type="danger"
              size="small"
              @click="deletePortMappingHandler(row.id)"
            >
              删除
            </el-button>
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

    <!-- 编辑端口映射对话框 -->
    <el-dialog
      v-model="editDialogVisible"
      title="编辑端口映射"
      width="600px"
    >
      <el-form
        ref="editFormRef"
        :model="editForm"
        :rules="editRules"
        label-width="120px"
      >
        <el-form-item
          label="实例名称"
          prop="instanceName"
        >
          <el-input
            v-model="editForm.instanceName"
            disabled
          />
        </el-form-item>
        <el-form-item
          label="公网端口"
          prop="hostPort"
        >
          <el-input-number
            v-model="editForm.hostPort"
            :min="1"
            :max="65535"
          />
        </el-form-item>
        <el-form-item
          label="内部端口"
          prop="guestPort"
        >
          <el-input-number
            v-model="editForm.guestPort"
            :min="1"
            :max="65535"
          />
        </el-form-item>
        <el-form-item
          label="协议"
          prop="protocol"
        >
          <el-select v-model="editForm.protocol">
            <el-option
              label="TCP"
              value="tcp"
            />
            <el-option
              label="UDP"
              value="udp"
            />
          </el-select>
        </el-form-item>
        <el-form-item
          label="状态"
          prop="status"
        >
          <el-select v-model="editForm.status">
            <el-option
              label="活跃"
              value="active"
            />
            <el-option
              label="未使用"
              value="inactive"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="editDialogVisible = false">取消</el-button>
          <el-button
            type="primary"
            @click="submitEdit"
          >确定</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 批量操作对话框 -->
    <el-dialog
      v-model="showBatchDialog"
      title="批量操作"
      width="500px"
    >
      <div>
        <p>已选择 {{ selectedPortMappings.length }} 个端口映射</p>
        <el-button
          type="danger"
          :disabled="selectedPortMappings.length === 0"
          @click="batchDelete"
        >
          批量删除
        </el-button>
        <el-button
          type="warning"
          :disabled="selectedPortMappings.length === 0"
          @click="batchUpdateStatus('inactive')"
        >
          批量停用
        </el-button>
        <el-button
          type="success"
          :disabled="selectedPortMappings.length === 0"
          @click="batchUpdateStatus('active')"
        >
          批量启用
        </el-button>
      </div>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="showBatchDialog = false">关闭</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getPortMappings, updatePortMapping, deletePortMapping, batchDeletePortMappings, getProviderList } from '@/api/admin'

// 响应式数据
const loading = ref(false)
const portMappings = ref([])
const providers = ref([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const selectedPortMappings = ref([])

// 搜索表单
const searchForm = reactive({
  keyword: '',
  providerId: '',
  status: ''
})

// 编辑对话框
const editDialogVisible = ref(false)
const editFormRef = ref()
const editForm = reactive({
  id: '',
  instanceName: '',
  hostPort: '',
  guestPort: '',
  protocol: 'tcp',
  status: 'active'
})

const editRules = {
  hostPort: [
    { required: true, message: '请输入公网端口', trigger: 'blur' },
    { type: 'number', min: 1, max: 65535, message: '端口范围为1-65535', trigger: 'blur' }
  ],
  guestPort: [
    { required: true, message: '请输入内部端口', trigger: 'blur' },
    { type: 'number', min: 1, max: 65535, message: '端口范围为1-65535', trigger: 'blur' }
  ],
  protocol: [
    { required: true, message: '请选择协议', trigger: 'change' }
  ],
  status: [
    { required: true, message: '请选择状态', trigger: 'change' }
  ]
}

// 批量操作对话框
const showBatchDialog = ref(false)

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
  } catch (error) {
    ElMessage.error('加载端口映射列表失败')
    console.error(error)
  } finally {
    loading.value = false
  }
}

const loadProviders = async () => {
  try {
    const response = await getProviderList({ page: 1, pageSize: 1000 })
    providers.value = response.data.items || []
  } catch (error) {
    console.error('加载Provider列表失败:', error)
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
    status: ''
  })
  searchPortMappings()
}

const handleSelectionChange = (selection) => {
  selectedPortMappings.value = selection
}

const editPortMapping = (row) => {
  Object.assign(editForm, {
    id: row.id,
    instanceName: row.instanceName,
    hostPort: row.hostPort,
    guestPort: row.guestPort,
    protocol: row.protocol,
    status: row.status
  })
  editDialogVisible.value = true
}

const submitEdit = async () => {
  if (!editFormRef.value) return
  
  try {
    await editFormRef.value.validate()
    await updatePortMapping(editForm.id, {
      hostPort: editForm.hostPort,
      guestPort: editForm.guestPort,
      protocol: editForm.protocol,
      status: editForm.status
    })
    ElMessage.success('更新成功')
    editDialogVisible.value = false
    loadPortMappings()
  } catch (error) {
    ElMessage.error('更新失败')
    console.error(error)
  }
}

const deletePortMappingHandler = async (id) => {
  try {
    await ElMessageBox.confirm('确定要删除这个端口映射吗？', '警告', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    })
    
    await deletePortMapping(id)
    ElMessage.success('删除成功')
    loadPortMappings()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('删除失败')
      console.error(error)
    }
  }
}

const batchDelete = async () => {
  if (selectedPortMappings.value.length === 0) {
    ElMessage.warning('请选择要删除的端口映射')
    return
  }
  
  try {
    await ElMessageBox.confirm(`确定要删除选中的 ${selectedPortMappings.value.length} 个端口映射吗？`, '警告', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    })
    
    const ids = selectedPortMappings.value.map(item => item.id)
    await batchDeletePortMappings(ids)
    ElMessage.success('批量删除成功')
    showBatchDialog.value = false
    loadPortMappings()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量删除失败')
      console.error(error)
    }
  }
}

const batchUpdateStatus = async (status) => {
  if (selectedPortMappings.value.length === 0) {
    ElMessage.warning('请选择要操作的端口映射')
    return
  }
  
  try {
    const promises = selectedPortMappings.value.map(item => 
      updatePortMapping(item.id, { status })
    )
    await Promise.all(promises)
    ElMessage.success(`批量${status === 'active' ? '启用' : '停用'}成功`)
    showBatchDialog.value = false
    loadPortMappings()
  } catch (error) {
    ElMessage.error(`批量${status === 'active' ? '启用' : '停用'}失败`)
    console.error(error)
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

// 生命周期
onMounted(() => {
  loadProviders()
  loadPortMappings()
})
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
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

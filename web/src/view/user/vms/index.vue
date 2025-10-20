<template>
  <div class="vms-container">
    <div class="page-header">
      <h1>虚拟机管理</h1>
      <p>管理您的虚拟机实例</p>
    </div>

    <!-- 操作栏 -->
    <div class="toolbar">
      <el-button
        type="primary"
        @click="showCreateDialog = true"
      >
        <el-icon><Plus /></el-icon>
        创建虚拟机
      </el-button>
      <el-button @click="refreshList">
        <el-icon><Refresh /></el-icon>
        刷新
      </el-button>
    </div>

    <!-- 虚拟机列表 -->
    <el-card>
      <el-table
        v-loading="loading"
        :data="vmList"
        style="width: 100%"
      >
        <el-table-column
          prop="name"
          label="名称"
          width="150"
        />
        <el-table-column
          prop="status"
          label="状态"
          width="100"
        >
          <template #default="scope">
            <el-tag :type="getStatusType(scope.row.status)">
              {{ getStatusText(scope.row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="provider"
          label="服务器"
          width="120"
        />
        <el-table-column
          prop="cpu"
          label="CPU"
          width="80"
        />
        <el-table-column
          prop="memory"
          label="内存"
          width="80"
        />
        <el-table-column
          prop="disk"
          label="磁盘"
          width="80"
        />
        <el-table-column
          prop="ip"
          label="IP地址"
          width="120"
        />
        <el-table-column
          prop="createdAt"
          label="创建时间"
          width="150"
        />
        <el-table-column
          label="操作"
          width="250"
        >
          <template #default="scope">
            <el-button-group size="small">
              <el-button 
                type="success" 
                :disabled="scope.row.status === 'running'" 
                @click="startVM(scope.row)"
              >
                启动
              </el-button>
              <el-button 
                type="warning" 
                :disabled="scope.row.status === 'stopped'" 
                @click="stopVM(scope.row)"
              >
                停止
              </el-button>
              <el-button 
                type="info" 
                :disabled="scope.row.status !== 'running'" 
                @click="restartVM(scope.row)"
              >
                重启
              </el-button>
              <el-button
                type="primary"
                @click="viewVM(scope.row)"
              >
                详情
              </el-button>
              <el-button
                type="danger"
                @click="deleteVM(scope.row)"
              >
                删除
              </el-button>
            </el-button-group>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-wrapper">
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.size"
          :page-sizes="[10, 20, 50, 100]"
          :total="pagination.total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>

    <!-- 创建虚拟机对话框 -->
    <el-dialog
      v-model="showCreateDialog"
      title="创建虚拟机"
      width="600px"
    >
      <el-form
        ref="createFormRef"
        :model="createForm"
        :rules="createRules"
        label-width="100px"
      >
        <el-form-item
          label="名称"
          prop="name"
        >
          <el-input
            v-model="createForm.name"
            placeholder="请输入虚拟机名称"
          />
        </el-form-item>
        
        <el-form-item
          label="节点"
          prop="providerId"
        >
          <el-select
            v-model="createForm.providerId"
            placeholder="请选择节点"
            style="width: 100%"
          >
            <el-option
              v-for="provider in availableProviders"
              :key="provider.id"
              :label="provider.name"
              :value="provider.id"
            />
          </el-select>
        </el-form-item>
        
        <el-form-item
          label="操作系统"
          prop="os"
        >
          <el-select
            v-model="createForm.os"
            placeholder="请选择操作系统"
            style="width: 100%"
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
        
        <el-form-item
          label="CPU核数"
          prop="cpu"
        >
          <el-select
            v-model="createForm.cpu"
            placeholder="请选择CPU核数"
            style="width: 100%"
          >
            <el-option
              label="1核"
              :value="1"
            />
            <el-option
              label="2核"
              :value="2"
            />
            <el-option
              label="4核"
              :value="4"
            />
            <el-option
              label="8核"
              :value="8"
            />
          </el-select>
        </el-form-item>
        
        <el-form-item
          label="内存"
          prop="memory"
        >
          <el-select
            v-model="createForm.memory"
            placeholder="请选择内存大小"
            style="width: 100%"
          >
            <el-option
              label="1GB"
              value="1G"
            />
            <el-option
              label="2GB"
              value="2G"
            />
            <el-option
              label="4GB"
              value="4G"
            />
            <el-option
              label="8GB"
              value="8G"
            />
            <el-option
              label="16GB"
              value="16G"
            />
          </el-select>
        </el-form-item>
        
        <el-form-item
          label="磁盘"
          prop="disk"
        >
          <el-select
            v-model="createForm.disk"
            placeholder="请选择磁盘大小"
            style="width: 100%"
          >
            <el-option
              label="20GB"
              value="20G"
            />
            <el-option
              label="40GB"
              value="40G"
            />
            <el-option
              label="80GB"
              value="80G"
            />
            <el-option
              label="160GB"
              value="160G"
            />
          </el-select>
        </el-form-item>
      </el-form>
      
      <template #footer>
        <el-button @click="showCreateDialog = false">
          取消
        </el-button>
        <el-button
          type="primary"
          :loading="creating"
          @click="createVM"
        >
          创建
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { getUserVMs, createUserVM, controlUserVM, deleteUserVM, getAvailableProviders } from '@/api/user'
import { getOperatingSystemsByCategory } from '@/utils/operating-systems'

const loading = ref(false)
const creating = ref(false)
const showCreateDialog = ref(false)
const createFormRef = ref()

const vmList = ref([])
const availableProviders = ref([])
const groupedOperatingSystems = ref(getOperatingSystemsByCategory())

const pagination = reactive({
  page: 1,
  size: 10,
  total: 0
})

const createForm = reactive({
  name: '',
  providerId: '',
  os: '',
  cpu: 1,
  memory: '1G',
  disk: '20G'
})

const createRules = reactive({
  name: [
    { required: true, message: '请输入虚拟机名称', trigger: 'blur' },
    { min: 2, max: 50, message: '名称长度在 2 到 50 个字符', trigger: 'blur' }
  ],
  providerId: [
    { required: true, message: '请选择节点', trigger: 'change' }
  ],
  os: [
    { required: true, message: '请选择操作系统', trigger: 'change' }
  ],
  cpu: [
    { required: true, message: '请选择CPU核数', trigger: 'change' }
  ],
  memory: [
    { required: true, message: '请选择内存大小', trigger: 'change' }
  ],
  disk: [
    { required: true, message: '请选择磁盘大小', trigger: 'change' }
  ]
})

const getStatusType = (status) => {
  switch (status) {
    case 'running': return 'success'
    case 'stopped': return 'info'
    case 'error': return 'danger'
    default: return 'warning'
  }
}

const getStatusText = (status) => {
  switch (status) {
    case 'running': return '运行中'
    case 'stopped': return '已停止'
    case 'error': return '异常'
    default: return '未知'
  }
}

const fetchVMList = async () => {
  loading.value = true
  try {
    const response = await getUserVMs({
      page: pagination.page,
      size: pagination.size
    })
    if (response.code === 0 || response.code === 200) {
      vmList.value = response.data.list
      pagination.total = response.data.total
    }
  } catch (error) {
    ElMessage.error('获取虚拟机列表失败')
  } finally {
    loading.value = false
  }
}

const fetchAvailableProviders = async () => {
  try {
    const response = await getAvailableProviders()
    if (response.code === 200) {
      availableProviders.value = response.data
    }
  } catch (error) {
    console.error('获取可用节点失败:', error)
  }
}

const refreshList = () => {
  fetchVMList()
}

const createVM = async () => {
  if (!createFormRef.value) return
  
  await createFormRef.value.validate(async (valid) => {
    if (!valid) return
    
    creating.value = true
    try {
      const response = await createUserVM(createForm)
      if (response.code === 0 || response.code === 200) {
        ElMessage.success('虚拟机创建成功')
        showCreateDialog.value = false
        resetCreateForm()
        fetchVMList()
      } else {
        ElMessage.error(response.msg || '创建失败')
      }
    } catch (error) {
      ElMessage.error('创建失败，请重试')
    } finally {
      creating.value = false
    }
  })
}

const startVM = async (vm) => {
  try {
    const response = await controlUserVM(vm.id, 'start')
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`虚拟机 ${vm.name} 启动成功`)
      fetchVMList()
    } else {
      ElMessage.error(response.msg || '启动失败')
    }
  } catch (error) {
    ElMessage.error('启动失败，请重试')
  }
}

const stopVM = async (vm) => {
  try {
    const response = await controlUserVM(vm.id, 'stop')
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`虚拟机 ${vm.name} 停止成功`)
      fetchVMList()
    } else {
      ElMessage.error(response.msg || '停止失败')
    }
  } catch (error) {
    ElMessage.error('停止失败，请重试')
  }
}

const restartVM = async (vm) => {
  try {
    const response = await controlUserVM(vm.id, 'restart')
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`虚拟机 ${vm.name} 重启成功`)
      fetchVMList()
    } else {
      ElMessage.error(response.msg || '重启失败')
    }
  } catch (error) {
    ElMessage.error('重启失败，请重试')
  }
}

const viewVM = (vm) => {
  ElMessage.info(`查看虚拟机 ${vm.name} 详情`)
}

const deleteVM = async (vm) => {
  try {
    await ElMessageBox.confirm(`确定要删除虚拟机 ${vm.name} 吗？此操作不可恢复！`, '确认删除', {
      type: 'warning'
    })
    
    const response = await deleteUserVM(vm.id)
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`虚拟机 ${vm.name} 删除成功`)
      fetchVMList()
    } else {
      ElMessage.error(response.msg || '删除失败')
    }
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('删除失败，请重试')
    }
  }
}

const resetCreateForm = () => {
  createForm.name = ''
  createForm.providerId = ''
  createForm.os = ''
  createForm.cpu = 1
  createForm.memory = '1G'
  createForm.disk = '20G'
  if (createFormRef.value) {
    createFormRef.value.clearValidate()
  }
}

const handleSizeChange = (size) => {
  pagination.size = size
  fetchVMList()
}

const handleCurrentChange = (page) => {
  pagination.page = page
  fetchVMList()
}

onMounted(() => {
  fetchVMList()
  fetchAvailableProviders()
})
</script>

<style scoped>
.vms-container {
  padding: 20px;
}

.page-header {
  margin-bottom: 30px;
}

.page-header h1 {
  margin: 0 0 10px 0;
  color: #333;
  font-size: 28px;
  font-weight: 600;
}

.page-header p {
  margin: 0;
  color: #666;
  font-size: 16px;
}

.toolbar {
  margin-bottom: 20px;
  display: flex;
  gap: 10px;
}

.pagination-wrapper {
  margin-top: 20px;
  text-align: right;
}
</style>
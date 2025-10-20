<template>
  <div class="containers-container">
    <div class="page-header">
      <h1>容器管理</h1>
      <p>管理您的容器实例</p>
    </div>

    <!-- 操作栏 -->
    <div class="toolbar">
      <el-button
        type="primary"
        @click="showCreateDialog = true"
      >
        <el-icon><Plus /></el-icon>
        创建容器
      </el-button>
      <el-button @click="refreshList">
        <el-icon><Refresh /></el-icon>
        刷新
      </el-button>
    </div>

    <!-- 容器列表 -->
    <el-card>
      <el-table
        v-loading="loading"
        :data="containerList"
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
          prop="image"
          label="镜像"
          width="200"
        />
        <el-table-column
          prop="provider"
          label="服务器"
          width="120"
        />
        <el-table-column
          prop="cpu"
          label="CPU限制"
          width="100"
        />
        <el-table-column
          prop="memory"
          label="内存限制"
          width="100"
        />
        <el-table-column
          prop="ports"
          label="端口映射"
          width="120"
        >
          <template #default="scope">
            <el-tag
              v-for="port in scope.row.ports"
              :key="port"
              size="small"
              style="margin-right: 5px;"
            >
              {{ port }}
            </el-tag>
          </template>
        </el-table-column>
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
                @click="startContainer(scope.row)"
              >
                启动
              </el-button>
              <el-button 
                type="warning" 
                :disabled="scope.row.status === 'stopped'" 
                @click="stopContainer(scope.row)"
              >
                停止
              </el-button>
              <el-button 
                type="info" 
                :disabled="scope.row.status !== 'running'" 
                @click="restartContainer(scope.row)"
              >
                重启
              </el-button>
              <el-button
                type="primary"
                @click="viewContainer(scope.row)"
              >
                详情
              </el-button>
              <el-button
                type="danger"
                @click="deleteContainer(scope.row)"
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

    <!-- 创建容器对话框 - 使用新的安全模式 -->
    <el-dialog
      v-model="showCreateDialog"
      title="创建容器"
      width="800px"
      @close="resetCreateForm"
    >
      <el-alert
        title="安全提示"
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 20px"
      >
        容器名称将由系统自动生成，所有配置选项基于您的配额动态过滤。
      </el-alert>

      <el-form
        ref="createFormRef"
        :model="createForm"
        :rules="createRules"
        label-width="120px"
      >
        <el-form-item
          label="节点"
          prop="providerId"
        >
          <el-select
            v-model="createForm.providerId"
            placeholder="请选择节点"
            style="width: 100%"
            @change="onProviderChange"
          >
            <el-option
              v-for="provider in availableProviders"
              :key="provider.id"
              :label="`${provider.name} (${provider.type})`"
              :value="provider.id"
            />
          </el-select>
        </el-form-item>
        
        <el-form-item
          label="镜像"
          prop="image"
        >
          <el-select
            v-model="createForm.image"
            placeholder="请选择镜像"
            style="width: 100%"
            filterable
          >
            <el-option-group
              v-for="group in instanceConfig.images"
              :key="group.label"
              :label="group.label"
            >
              <el-option
                v-for="option in group.options"
                :key="option.value"
                :label="option.label"
                :value="option.value"
              />
            </el-option-group>
          </el-select>
        </el-form-item>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              label="CPU规格"
              prop="cpu"
            >
              <el-select
                v-model="createForm.cpu"
                placeholder="选择CPU规格"
              >
                <el-option 
                  v-for="cpu in instanceConfig.cpuSpecs" 
                  :key="cpu.value" 
                  :label="cpu.label" 
                  :value="cpu.value"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="内存规格"
              prop="memory"
            >
              <el-select
                v-model="createForm.memory"
                placeholder="选择内存规格"
              >
                <el-option 
                  v-for="memory in instanceConfig.memorySpecs" 
                  :key="memory.value" 
                  :label="memory.label" 
                  :value="memory.value"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              label="磁盘规格"
              prop="disk"
            >
              <el-select
                v-model="createForm.disk"
                placeholder="选择磁盘规格"
              >
                <el-option 
                  v-for="disk in instanceConfig.diskSpecs" 
                  :key="disk.value" 
                  :label="disk.label" 
                  :value="disk.value"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="带宽规格"
              prop="bandwidth"
            >
              <el-select
                v-model="createForm.bandwidth"
                placeholder="选择带宽规格"
              >
                <el-option 
                  v-for="bandwidth in instanceConfig.bandwidthSpecs" 
                  :key="bandwidth.value" 
                  :label="bandwidth.label" 
                  :value="bandwidth.value"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="描述">
          <el-input 
            v-model="createForm.description"
            type="textarea"
            :rows="3"
            placeholder="请输入容器描述（可选）"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>
      </el-form>
      
      <template #footer>
        <el-button @click="showCreateDialog = false">
          取消
        </el-button>
        <el-button
          type="primary"
          :loading="creating"
          @click="createContainer"
        >
          创建容器
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { getUserContainers, createUserContainer, controlUserContainer, deleteUserContainer, getAvailableProviders, getInstanceConfig } from '@/api/user'

const loading = ref(false)
const creating = ref(false)
const showCreateDialog = ref(false)
const createFormRef = ref()

const containerList = ref([])
const availableProviders = ref([])
const instanceConfig = ref({
  images: [],
  cpuSpecs: [],
  memorySpecs: [],
  diskSpecs: [],
  bandwidthSpecs: []
})

const pagination = reactive({
  page: 1,
  size: 10,
  total: 0
})

const createForm = reactive({
  providerId: '',
  image: '',
  cpu: '',
  memory: '',
  disk: '',
  bandwidth: '',
  ports: '',
  env: '',
  description: ''
})

const createRules = reactive({
  providerId: [
    { required: true, message: '请选择节点', trigger: 'change' }
  ],
  image: [
    { required: true, message: '请选择镜像', trigger: 'change' }
  ],
  cpu: [
    { required: true, message: '请选择CPU规格', trigger: 'change' }
  ],
  memory: [
    { required: true, message: '请选择内存规格', trigger: 'change' }
  ],
  disk: [
    { required: true, message: '请选择磁盘规格', trigger: 'change' }
  ],
  bandwidth: [
    { required: true, message: '请选择带宽规格', trigger: 'change' }
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

const fetchContainerList = async () => {
  loading.value = true
  try {
    const response = await getUserContainers({
      page: pagination.page,
      size: pagination.size
    })
    if (response.code === 0 || response.code === 200) {
      containerList.value = response.data.list
      pagination.total = response.data.total
    }
  } catch (error) {
    ElMessage.error('获取容器列表失败')
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

const fetchInstanceConfig = async () => {
  try {
    const response = await getInstanceConfig()
    if (response.code === 0 || response.code === 200) {
      instanceConfig.value = response.data || {
        images: [],
        cpuSpecs: [],
        memorySpecs: [],
        diskSpecs: [],
        bandwidthSpecs: []
      }
    }
  } catch (error) {
    ElMessage.error('获取配置选项失败')
  }
}

// 节点变化处理
const onProviderChange = () => {
  // 节点变化时可以清空某些字段或重新加载相关数据
  createForm.image = ''
}

const refreshList = () => {
  fetchContainerList()
}

const createContainer = async () => {
  if (!createFormRef.value) return
  
  await createFormRef.value.validate(async (valid) => {
    if (!valid) return
    
    creating.value = true
    try {
      const response = await createUserContainer(createForm)
      if (response.code === 0 || response.code === 200) {
        ElMessage.success('容器创建成功')
        showCreateDialog.value = false
        resetCreateForm()
        fetchContainerList()
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

const startContainer = async (container) => {
  try {
    const response = await controlUserContainer(container.id, 'start')
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`容器 ${container.name} 启动成功`)
      fetchContainerList()
    } else {
      ElMessage.error(response.msg || '启动失败')
    }
  } catch (error) {
    ElMessage.error('启动失败，请重试')
  }
}

const stopContainer = async (container) => {
  try {
    const response = await controlUserContainer(container.id, 'stop')
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`容器 ${container.name} 停止成功`)
      fetchContainerList()
    } else {
      ElMessage.error(response.msg || '停止失败')
    }
  } catch (error) {
    ElMessage.error('停止失败，请重试')
  }
}

const restartContainer = async (container) => {
  try {
    const response = await controlUserContainer(container.id, 'restart')
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`容器 ${container.name} 重启成功`)
      fetchContainerList()
    } else {
      ElMessage.error(response.msg || '重启失败')
    }
  } catch (error) {
    ElMessage.error('重启失败，请重试')
  }
}

const viewContainer = (container) => {
  ElMessage.info(`查看容器 ${container.name} 详情`)
}

const deleteContainer = async (container) => {
  try {
    await ElMessageBox.confirm(`确定要删除容器 ${container.name} 吗？此操作不可恢复！`, '确认删除', {
      type: 'warning'
    })
    
    const response = await deleteUserContainer(container.id)
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`容器 ${container.name} 删除成功`)
      fetchContainerList()
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
  createForm.providerId = ''
  createForm.image = ''
  createForm.cpu = ''
  createForm.memory = ''
  createForm.disk = ''
  createForm.bandwidth = ''
  createForm.ports = ''
  createForm.env = ''
  createForm.description = ''
  if (createFormRef.value) {
    createFormRef.value.clearValidate()
  }
}

const handleSizeChange = (size) => {
  pagination.size = size
  fetchContainerList()
}

const handleCurrentChange = (page) => {
  pagination.page = page
  fetchContainerList()
}

onMounted(() => {
  fetchContainerList()
  fetchAvailableProviders()
  fetchInstanceConfig()
})
</script>

<style scoped>
.containers-container {
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

.form-tip {
  font-size: 12px;
  color: #999;
  margin-top: 5px;
}
</style>
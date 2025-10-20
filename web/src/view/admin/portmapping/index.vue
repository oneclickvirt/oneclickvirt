<template>
  <div>
    <el-card class="box-card">
      <template #header>
        <div class="card-header">
          <span>端口映射管理</span>
          <div class="header-actions">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              style="margin-right: 10px;"
            >
              <template #title>
                <span style="font-size: 12px;">
                  区间映射端口随实例创建，不可删除；手动添加的端口可以删除
                </span>
              </template>
            </el-alert>
            <el-button
              type="primary"
              @click="openAddDialog"
            >
              <el-icon><Plus /></el-icon>
              手动添加端口
            </el-button>
            <el-button
              v-if="selectedPortMappings.length > 0"
              type="danger"
              @click="batchDeleteDirect"
            >
              批量删除 ({{ selectedPortMappings.length }})
            </el-button>
          </div>
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
          :selectable="isManualPort"
        />
        <el-table-column
          prop="id"
          label="ID"
          width="80"
        />
        <el-table-column
          prop="portType"
          label="端口类型"
          width="120"
        >
          <template #default="{ row }">
            <el-tag :type="row.portType === 'manual' ? 'warning' : 'success'">
              {{ row.portType === 'manual' ? '手动添加' : '区间映射' }}
            </el-tag>
          </template>
        </el-table-column>
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
          prop="description"
          label="描述"
          width="120"
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
          width="120"
        >
          <template #default="{ row }">
            <el-tag 
              v-if="row.status === 'active'" 
              type="success"
            >
              活跃
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'creating'" 
              type="warning"
            >
              <el-icon class="is-loading"><Loading /></el-icon>
              创建中
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'failed'" 
              type="danger"
            >
              失败
            </el-tag>
            <el-tag 
              v-else 
              type="info"
            >
              {{ row.status || '未知' }}
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
              删除
            </el-button>
            <el-tooltip
              v-else
              content="区间映射端口不可删除"
              placement="top"
            >
              <el-button
                type="info"
                size="small"
                disabled
              >
                不可删除
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
      title="手动添加端口映射"
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
            仅支持为 LXD/Incus/Proxmox 实例手动添加端口，Docker 不支持
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
          label="选择实例"
          prop="instanceId"
        >
          <el-select
            v-model="addForm.instanceId"
            placeholder="请输入实例名称或ID搜索"
            filterable
            clearable
            style="width: 100%"
            @change="onInstanceChange"
            :filter-method="filterInstances"
            :no-data-text="instances.length === 0 ? '暂无实例数据' : '没有匹配的实例'"
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
            <span v-if="selectedInstanceProvider !== '-'">
              当前实例 Provider: <strong>{{ selectedInstanceProvider }}</strong>
            </span>
            <span v-else>请选择一个实例</span>
          </div>
        </el-form-item>
        
        <el-form-item
          label="内部端口"
          prop="guestPort"
        >
          <el-input-number
            v-model="addForm.guestPort"
            :min="1"
            :max="65535"
            placeholder="请输入容器/虚拟机内部端口"
            style="width: 100%"
          />
        </el-form-item>
        
        <el-form-item
          label="公网端口"
          prop="hostPort"
        >
          <el-input-number
            v-model="addForm.hostPort"
            :min="0"
            :max="65535"
            placeholder="0 表示自动分配"
            style="width: 100%"
          />
          <div style="color: #909399; font-size: 12px; margin-top: 5px;">
            留空或填0将自动分配可用端口
          </div>
        </el-form-item>
        
        <el-form-item
          label="协议"
          prop="protocol"
        >
          <el-radio-group v-model="addForm.protocol">
            <el-radio label="tcp">TCP</el-radio>
            <el-radio label="udp">UDP</el-radio>
          </el-radio-group>
        </el-form-item>
        
        <el-form-item
          label="描述"
          prop="description"
        >
          <el-input
            v-model="addForm.description"
            placeholder="端口用途说明（可选）"
            maxlength="128"
            show-word-limit
          />
        </el-form-item>
      </el-form>
      
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="addDialogVisible = false">取消</el-button>
          <el-button
            type="primary"
            :loading="addLoading"
            @click="submitAdd"
          >
            确定添加
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onUnmounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Loading } from '@element-plus/icons-vue'
import { 
  getPortMappings, 
  createPortMapping,
  deletePortMapping, 
  batchDeletePortMappings, 
  getProviderList,
  getAllInstances
} from '@/api/admin'

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
  status: ''
})

// 添加端口对话框
const addDialogVisible = ref(false)
const addFormRef = ref()
const addLoading = ref(false)
const addForm = reactive({
  instanceId: '',
  guestPort: null,
  hostPort: 0,
  protocol: 'tcp',
  description: ''
})

const addRules = {
  instanceId: [
    { required: true, message: '请选择实例', trigger: 'change' }
  ],
  guestPort: [
    { required: true, message: '请输入内部端口', trigger: 'blur' },
    { type: 'number', min: 1, max: 65535, message: '端口范围为1-65535', trigger: 'blur' }
  ],
  protocol: [
    { required: true, message: '请选择协议', trigger: 'change' }
  ]
}

// 获取实例对应的 Provider 类型
const getInstanceProviderType = (instance) => {
  if (!instance || !instance.providerId) return null
  const provider = providers.value.find(p => p.id === instance.providerId)
  return provider ? provider.type : null
}

// 过滤支持的实例（仅 LXD/Incus/Proxmox）
const supportedInstances = computed(() => {
  const filtered = instances.value.filter(instance => {
    const type = getInstanceProviderType(instance)?.toLowerCase()
    return type === 'lxd' || type === 'incus' || type === 'proxmox'
  })
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

// 实例过滤状态
const instanceFilterText = ref('')
const filteredInstances = computed(() => {
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
    ElMessage.error('加载端口映射列表失败')
    console.error(error)
  } finally {
    loading.value = false
  }
}

// 检查是否需要自动刷新
const checkAndStartAutoRefresh = () => {
  const hasCreatingPorts = portMappings.value.some(port => port.status === 'creating')
  
  if (hasCreatingPorts) {
    // 如果有正在创建的端口，启动自动刷新（每5秒刷新一次）
    if (!autoRefreshTimer) {
      console.log('检测到创建中的端口，启动自动刷新')
      autoRefreshTimer = setInterval(() => {
        loadPortMappings()
      }, 5000)
    }
  } else {
    // 没有正在创建的端口，停止自动刷新
    if (autoRefreshTimer) {
      console.log('所有端口已完成，停止自动刷新')
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
    ElMessage.error('加载Provider列表失败')
  }
}

const loadInstances = async () => {
  try {
    const response = await getAllInstances({ page: 1, pageSize: 1000 })
    instances.value = response.data.list || []
  } catch (error) {
    ElMessage.error('加载实例列表失败')
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
      '确定要删除这个手动添加的端口映射吗？删除后将从远程服务器上移除端口映射。', 
      '警告', 
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    await deletePortMapping(id)
    ElMessage.success('删除成功')
    loadPortMappings()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '删除失败')
    }
  }
}

// 批量删除（仅删除手动添加的端口）
const batchDeleteDirect = async () => {
  if (selectedPortMappings.value.length === 0) {
    ElMessage.warning('请选择要删除的端口映射')
    return
  }
  
  // 检查是否都是手动添加的端口
  const hasRangeMappedPort = selectedPortMappings.value.some(item => item.portType !== 'manual')
  if (hasRangeMappedPort) {
    ElMessage.warning('只能删除手动添加的端口，区间映射端口不能删除')
    return
  }
  
  try {
    await ElMessageBox.confirm(
      `确定要删除选中的 ${selectedPortMappings.value.length} 个手动添加的端口映射吗？删除后将从远程服务器上移除端口映射，此操作不可恢复。`, 
      '批量删除端口映射', 
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    const ids = selectedPortMappings.value.map(item => item.id)
    await batchDeletePortMappings(ids)
    ElMessage.success(`成功删除 ${selectedPortMappings.value.length} 个端口映射`)
    selectedPortMappings.value = []
    loadPortMappings()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '批量删除失败')
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
    protocol: 'tcp',
    description: ''
  })
  
  // 如果实例列表为空，重新加载
  if (instances.value.length === 0) {
    await loadInstances()
  }
  
  if (supportedInstances.value.length === 0) {
    ElMessage.warning('暂无可用的 LXD/Incus/Proxmox 实例')
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
      ElMessage.error('未找到选中的实例')
      return
    }
    
    const providerType = getInstanceProviderType(instance)?.toLowerCase()
    if (providerType === 'docker') {
      ElMessage.error('Docker 实例不支持手动添加端口')
      return
    }
    
    if (!['lxd', 'incus', 'proxmox'].includes(providerType)) {
      ElMessage.error('只支持 LXD/Incus/Proxmox 实例手动添加端口')
      return
    }
    
    addLoading.value = true
    
    const data = {
      instanceId: addForm.instanceId,
      guestPort: addForm.guestPort,
      hostPort: addForm.hostPort || 0,
      protocol: addForm.protocol,
      description: addForm.description
    }
    
    const response = await createPortMapping(data)
    ElMessage.success('端口映射任务已创建，正在后台配置远程服务器，请稍后刷新查看状态')
    addDialogVisible.value = false
    loadPortMappings()
    loadPortMappings()
  } catch (error) {
    ElMessage.error(error.message || '添加端口失败')
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

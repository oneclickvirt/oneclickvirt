<template>
  <div class="instances-container">
    <el-card>
      <template #header>
        <div class="header-row">
          <span>实例管理</span>
          <el-button
            type="primary"
            :loading="loading"
            @click="loadInstances"
          >
            刷新
          </el-button>
        </div>
      </template>

      <!-- 筛选条件 -->
      <div class="filter-row">
        <el-input
          v-model="filters.providerName"
          placeholder="按节点名称搜索"
          style="width: 200px; margin-right: 10px;"
          clearable
        />
        <el-select
          v-model="filters.status"
          placeholder="状态筛选"
          style="width: 120px; margin-right: 10px;"
          clearable
        >
          <el-option
            label="运行中"
            value="running"
          />
          <el-option
            label="已停止"
            value="stopped"
          />
          <el-option
            label="创建中"
            value="creating"
          />
          <el-option
            label="启动中"
            value="starting"
          />
          <el-option
            label="停止中"
            value="stopping"
          />
          <el-option
            label="错误"
            value="error"
          />
        </el-select>
        <el-select
          v-model="filters.instanceType"
          placeholder="类型筛选"
          style="width: 120px; margin-right: 10px;"
          clearable
        >
          <el-option
            label="容器"
            value="container"
          />
          <el-option
            label="虚拟机"
            value="vm"
          />
        </el-select>
        <el-button
          type="primary"
          @click="handleSearch"
        >
          搜索
        </el-button>
        <el-button
          @click="handleReset"
        >
          重置
        </el-button>
      </div>

      <el-table
        v-loading="loading"
        :data="instances"
        style="width: 100%"
        row-key="id"
      >
        <el-table-column
          prop="name"
          label="实例名称"
          min-width="140"
          show-overflow-tooltip
        />
        <el-table-column
          prop="userName"
          label="所有者"
          width="100"
        />
        <el-table-column
          prop="providerName"
          label="Provider"
          width="120"
          show-overflow-tooltip
        />
        <el-table-column
          prop="instance_type"
          label="类型"
          width="80"
        >
          <template #default="scope">
            <el-tag
              :type="scope.row.instance_type === 'container' ? 'primary' : 'success'"
              size="small"
            >
              {{ scope.row.instance_type === 'container' ? '容器' : '虚拟机' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          label="状态"
          width="100"
        >
          <template #default="scope">
            <el-tag
              :type="getStatusType(scope.row.status)"
              size="small"
            >
              {{ getStatusText(scope.row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          label="规格"
          width="120"
        >
          <template #default="scope">
            <div style="font-size: 12px;">
              <div>CPU: {{ scope.row.cpu }}核</div>
              <div>内存: {{ formatMemory(scope.row.memory) }}</div>
              <div>磁盘: {{ formatDisk(scope.row.disk) }}</div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="ipAddress"
          label="IP地址"
          width="130"
          show-overflow-tooltip
        />
        <el-table-column
          prop="sshPort"
          label="SSH端口"
          width="80"
        />
        <el-table-column
          prop="osType"
          label="系统"
          width="80"
        />
        <el-table-column
          label="流量状态"
          width="100"
        >
          <template #default="scope">
            <el-tag
              v-if="scope.row.trafficLimited"
              type="danger"
              size="small"
            >
              已限制
            </el-tag>
            <el-tag
              v-else
              type="success"
              size="small"
            >
              正常
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          label="创建时间"
          width="140"
        >
          <template #default="scope">
            {{ formatDate(scope.row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          prop="expiredAt"
          label="到期时间"
          width="140"
        >
          <template #default="scope">
            <span :class="{ 'expired': isExpired(scope.row.expiredAt), 'expiring-soon': isExpiringSoon(scope.row.expiredAt) }">
              {{ formatDate(scope.row.expiredAt) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          label="操作"
          width="220"
          fixed="right"
        >
          <template #default="scope">
            <div class="action-buttons">
              <el-button
                size="small"
                :disabled="scope.row.status === 'running' || scope.row.status === 'starting'"
                @click="manageInstance(scope.row, 'start')"
              >
                启动
              </el-button>
              <el-button
                size="small"
                :disabled="scope.row.status === 'stopped' || scope.row.status === 'stopping'"
                @click="manageInstance(scope.row, 'stop')"
              >
                停止
              </el-button>
              <el-button
                size="small"
                :disabled="scope.row.status !== 'running'"
                @click="manageInstance(scope.row, 'restart')"
              >
                重启
              </el-button>
              <el-button
                size="small"
                type="primary"
                :disabled="scope.row.status !== 'running'"
                @click="showResetPasswordDialog(scope.row)"
              >
                重置密码
              </el-button>
              <el-button
                size="small"
                type="primary"
                @click="viewInstanceDetail(scope.row)"
              >
                详情
              </el-button>
              <el-button
                size="small"
                type="danger"
                @click="deleteInstance(scope.row.id)"
              >
                删除
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-row">
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

    <!-- 实例详情对话框 -->
    <el-dialog
      v-model="detailDialogVisible"
      title="实例详情"
      width="60%"
    >
      <div
        v-if="selectedInstance"
        class="instance-detail"
      >
        <el-descriptions
          :column="2"
          border
        >
          <el-descriptions-item label="实例名称">
            {{ selectedInstance.name }}
          </el-descriptions-item>
          <el-descriptions-item label="UUID">
            {{ selectedInstance.uuid }}
          </el-descriptions-item>
          <el-descriptions-item label="所有者">
            {{ selectedInstance.userName }}
          </el-descriptions-item>
          <el-descriptions-item label="Provider">
            {{ selectedInstance.providerName }}
          </el-descriptions-item>
          <el-descriptions-item label="实例类型">
            <el-tag :type="selectedInstance.instance_type === 'container' ? 'primary' : 'success'">
              {{ selectedInstance.instance_type === 'container' ? '容器' : '虚拟机' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="getStatusType(selectedInstance.status)">
              {{ getStatusText(selectedInstance.status) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="镜像">
            {{ selectedInstance.image }}
          </el-descriptions-item>
          <el-descriptions-item label="操作系统">
            {{ selectedInstance.osType }}
          </el-descriptions-item>
          <el-descriptions-item label="CPU">
            {{ selectedInstance.cpu }}核
          </el-descriptions-item>
          <el-descriptions-item label="内存">
            {{ formatMemory(selectedInstance.memory) }}
          </el-descriptions-item>
          <el-descriptions-item label="磁盘">
            {{ formatDisk(selectedInstance.disk) }}
          </el-descriptions-item>
          <el-descriptions-item label="带宽">
            {{ selectedInstance.bandwidth }}Mbps
          </el-descriptions-item>
          <el-descriptions-item label="公网IPv4">
            {{ selectedInstance.publicIP || '未分配' }}
          </el-descriptions-item>
          <el-descriptions-item label="内网IPv4">
            {{ selectedInstance.privateIP || '未分配' }}
          </el-descriptions-item>
          <el-descriptions-item label="内网IPv6" v-if="selectedInstance.ipv6Address">
            {{ selectedInstance.ipv6Address }}
          </el-descriptions-item>
          <el-descriptions-item label="公网IPv6" v-if="selectedInstance.publicIPv6">
            {{ selectedInstance.publicIPv6 }}
          </el-descriptions-item>
          <el-descriptions-item label="SSH端口">
            {{ selectedInstance.sshPort }}
          </el-descriptions-item>
          <el-descriptions-item label="用户名">
            {{ selectedInstance.username }}
          </el-descriptions-item>
          <el-descriptions-item label="密码">
            <span v-if="showPassword">{{ selectedInstance.password }}</span>
            <span v-else>••••••••</span>
            <el-button
              link
              @click="showPassword = !showPassword"
            >
              {{ showPassword ? '隐藏' : '显示' }}
            </el-button>
          </el-descriptions-item>
          <el-descriptions-item label="流量限制">
            <el-tag
              v-if="selectedInstance.trafficLimited"
              type="danger"
            >
              已限制
            </el-tag>
            <el-tag
              v-else
              type="success"
            >
              正常
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="vnstat接口">
            {{ selectedInstance.vnstatInterface || '未设置' }}
          </el-descriptions-item>
          <el-descriptions-item label="创建时间">
            {{ formatDate(selectedInstance.createdAt) }}
          </el-descriptions-item>
          <el-descriptions-item label="更新时间">
            {{ formatDate(selectedInstance.updatedAt) }}
          </el-descriptions-item>
          <el-descriptions-item label="到期时间">
            <span :class="{ 'expired': isExpired(selectedInstance.expiredAt), 'expiring-soon': isExpiringSoon(selectedInstance.expiredAt) }">
              {{ formatDate(selectedInstance.expiredAt) }}
            </span>
          </el-descriptions-item>
          <el-descriptions-item label="健康状态">
            <el-tag :type="selectedInstance.healthStatus === 'healthy' ? 'success' : 'danger'">
              {{ selectedInstance.healthStatus === 'healthy' ? '健康' : '异常' }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>

        <div
          class="traffic-info"
          style="margin-top: 20px;"
        >
          <h4>流量使用情况</h4>
          <el-descriptions
            :column="2"
            border
          >
            <el-descriptions-item label="入站流量">
              {{ formatTraffic(selectedInstance.usedTrafficIn) }}
            </el-descriptions-item>
            <el-descriptions-item label="出站流量">
              {{ formatTraffic(selectedInstance.usedTrafficOut) }}
            </el-descriptions-item>
          </el-descriptions>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getAllInstances, deleteInstance as deleteInstanceApi, adminInstanceAction, resetInstancePassword } from '@/api/admin'

const instances = ref([])
const loading = ref(false)
const detailDialogVisible = ref(false)
const selectedInstance = ref(null)
const showPassword = ref(false)

// 筛选条件
const filters = ref({
  providerName: '',
  status: '',
  instanceType: ''
})

// 分页
const pagination = ref({
  page: 1,
  pageSize: 10,
  total: 0
})

const loadInstances = async () => {
  loading.value = true
  try {
    const params = {
      page: pagination.value.page,
      pageSize: pagination.value.pageSize,
      providerName: filters.value.providerName || undefined,
      status: filters.value.status || undefined,
      instance_type: filters.value.instanceType || undefined
    }
    
    // 移除undefined值
    Object.keys(params).forEach(key => {
      if (params[key] === undefined) {
        delete params[key]
      }
    })
    
    const response = await getAllInstances(params)
    instances.value = response.data.list || []
    pagination.value.total = response.data.total || 0
  } catch (error) {
    ElMessage.error('加载实例列表失败')
    console.error('Load instances error:', error)
  } finally {
    loading.value = false
  }
}

const handleSearch = () => {
  pagination.value.page = 1
  loadInstances()
}

const handleReset = () => {
  filters.value.providerName = ''
  filters.value.status = ''
  filters.value.instanceType = ''
  pagination.value.page = 1
  loadInstances()
}

const handleSizeChange = (val) => {
  pagination.value.pageSize = val
  pagination.value.page = 1
  loadInstances()
}

const handleCurrentChange = (val) => {
  pagination.value.page = val
  loadInstances()
}

const viewInstanceDetail = (instance) => {
  selectedInstance.value = instance
  showPassword.value = false
  detailDialogVisible.value = true
}

const getStatusType = (status) => {
  const types = {
    running: 'success',
    stopped: 'info',
    error: 'danger',
    failed: 'danger',
    starting: 'warning',
    stopping: 'warning',
    creating: 'warning',
    restarting: 'warning',
    deleting: 'danger'
  }
  return types[status] || 'info'
}

const getStatusText = (status) => {
  const texts = {
    running: '运行中',
    stopped: '已停止',
    error: '错误',
    failed: '创建失败',
    starting: '启动中',
    stopping: '停止中',
    creating: '创建中',
    restarting: '重启中',
    deleting: '删除中'
  }
  return texts[status] || status
}

const formatDate = (dateString) => {
  if (!dateString) return '未设置'
  const date = new Date(dateString)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  })
}

const formatMemory = (memory) => {
  if (!memory) return '0MB'
  if (memory >= 1024) {
    return `${(memory / 1024).toFixed(1)}GB`
  }
  return `${memory}MB`
}

const formatDisk = (disk) => {
  if (!disk) return '0MB'
  if (disk >= 1024 * 1024) {
    return `${(disk / (1024 * 1024)).toFixed(1)}TB`
  } else if (disk >= 1024) {
    return `${(disk / 1024).toFixed(1)}GB`
  }
  return `${disk}MB`
}

const formatTraffic = (traffic) => {
  if (!traffic) return '0MB'
  if (traffic >= 1024 * 1024) {
    return `${(traffic / (1024 * 1024)).toFixed(2)}TB`
  } else if (traffic >= 1024) {
    return `${(traffic / 1024).toFixed(2)}GB`
  }
  return `${traffic}MB`
}

const isExpired = (expiredAt) => {
  if (!expiredAt) return false
  return new Date(expiredAt) < new Date()
}

const isExpiringSoon = (expiredAt) => {
  if (!expiredAt) return false
  const expireDate = new Date(expiredAt)
  const now = new Date()
  const daysDiff = (expireDate - now) / (1000 * 60 * 60 * 24)
  return daysDiff > 0 && daysDiff <= 7 // 7天内到期
}

const manageInstance = async (instance, action) => {
  const actionText = {
    'start': '启动',
    'stop': '停止',
    'restart': '重启',
    'reset': '重置'
  }[action]
  
  try {
    await ElMessageBox.confirm(
      `确定${actionText}实例 "${instance.name}" 吗？操作将以任务形式异步执行。`,
      `${actionText}实例`,
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    await adminInstanceAction(instance.id, action)
    ElMessage.success(`${actionText}任务已创建`)
    await loadInstances()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(`${actionText}失败`)
    }
  }
}

// 显示重置密码对话框
const showResetPasswordDialog = async (instance) => {
  try {
    await ElMessageBox.confirm(
      `确定要重置实例 "${instance.name}" 的密码吗？\n系统将创建异步任务来执行密码重置操作。`,
      '重置实例密码',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    try {
      const response = await resetInstancePassword(instance.id)

      if (response.code === 0 || response.code === 200) {
        const taskId = response.data.taskId
        
        ElMessage.success(`密码重置任务已创建（任务ID: ${taskId}），请在任务列表中查看进度`)
        
        // 刷新实例列表
        await loadInstances()
      } else {
        ElMessage.error(response.message || '创建密码重置任务失败')
      }
    } catch (error) {
      console.error('创建密码重置任务失败:', error)
      ElMessage.error('创建密码重置任务失败，请稍后重试')
    }
  } catch (error) {
    // 用户取消操作
  }
}

const deleteInstance = async (id) => {
  try {
    await ElMessageBox.confirm(
      '确定删除该实例吗？删除操作将以任务形式异步执行，请在任务列表中查看进度。',
      '删除实例',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    await adminInstanceAction(id, 'delete')
    ElMessage.success('删除任务已创建，请查看任务列表了解进度')
    await loadInstances()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('删除失败')
    }
  }
}

onMounted(() => {
  loadInstances()
})
</script>

<style scoped>
.instances-container {
  padding: 20px;
}

.header-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.filter-row {
  margin-bottom: 20px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.action-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.action-buttons .el-button {
  margin: 0;
}

.pagination-row {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.instance-detail {
  max-height: 70vh;
  overflow-y: auto;
}

.traffic-info {
  border-top: 1px solid #ebeef5;
  padding-top: 20px;
}

.expired {
  color: #f56c6c;
  font-weight: bold;
}

.expiring-soon {
  color: #e6a23c;
  font-weight: bold;
}

/* 响应式设计 */
@media (max-width: 1200px) {
  .action-buttons {
    flex-direction: column;
  }
  
  .action-buttons .el-button {
    width: 100%;
    margin-bottom: 2px;
  }
}

@media (max-width: 768px) {
  .filter-row {
    flex-direction: column;
    align-items: stretch;
  }
  
  .filter-row > * {
    width: 100% !important;
    margin-bottom: 10px;
  }
}
</style>
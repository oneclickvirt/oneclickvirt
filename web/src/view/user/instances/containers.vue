<template>
  <div class="user-containers">
    <div class="page-header">
      <h2>容器管理</h2>
      <div class="header-actions">
        <el-button
          type="primary"
          @click="$router.push('/user/resources')"
        >
          <el-icon><Plus /></el-icon>
          申领新容器
        </el-button>
        <el-button @click="() => loadInstances(true)">
          <el-icon><Refresh /></el-icon>
          刷新
        </el-button>
      </div>
    </div>

    <!-- 筛选条件 -->
    <el-card class="filter-card">
      <el-form
        :model="filterForm"
        inline
      >
        <el-form-item>
          <el-select
            v-model="filterForm.status"
            placeholder="选择状态"
            clearable
            style="width: 150px;"
          >
            <el-option
              label="全部"
              value=""
            />
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
              label="删除中"
              value="deleting"
            />
          </el-select>
        </el-form-item>
        
        <el-form-item>
          <el-select
            v-model="filterForm.region"
            placeholder="选择地区"
            clearable
            style="width: 150px;"
          >
            <el-option
              label="全部"
              value=""
            />
            <el-option
              label="亚洲"
              value="asia"
            />
            <el-option
              label="欧洲"
              value="europe"
            />
            <el-option
              label="北美"
              value="north-america"
            />
          </el-select>
        </el-form-item>
        
        <el-form-item>
          <el-button
            type="primary"
            @click="() => loadInstances(true)"
          >
            搜索
          </el-button>
          <el-button @click="resetFilter">
            重置
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 容器列表 -->
    <el-card class="instances-card">
      <el-table 
        v-loading="loading" 
        :data="instances"
        style="width: 100%"
      >
        <el-table-column
          prop="name"
          label="容器名称"
          min-width="150"
        >
          <template #default="{ row }">
            <div class="instance-name">
              <el-icon><Box /></el-icon>
              <span>{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        
        <el-table-column
          prop="status"
          label="状态"
          width="100"
        >
          <template #default="{ row }">
            <el-tag :type="getStatusType(row.status)">
              {{ getStatusText(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        
        <el-table-column
          label="配置"
          width="120"
        >
          <template #default="{ row }">
            <div class="spec-info">
              <div>{{ row.cpu }}核</div>
              <div>{{ row.memory }}GB</div>
            </div>
          </template>
        </el-table-column>
        
        <el-table-column
          prop="region"
          label="地区"
          width="100"
        />
        
        <el-table-column
          label="IP地址"
          width="140"
        >
          <template #default="{ row }">
            <div v-if="row.ipAddress">
              <el-button 
                type="text" 
                size="small"
                @click="copyToClipboard(row.ipAddress)"
              >
                {{ row.ipAddress }}
              </el-button>
            </div>
            <span
              v-else
              class="text-muted"
            >-</span>
          </template>
        </el-table-column>
        
        <el-table-column
          label="端口"
          width="100"
        >
          <template #default="{ row }">
            <div v-if="row.sshPort">
              <el-tag size="small">
                SSH: {{ row.sshPort }}
              </el-tag>
            </div>
            <span
              v-else
              class="text-muted"
            >-</span>
          </template>
        </el-table-column>
        
        <el-table-column
          prop="createdAt"
          label="创建时间"
          width="160"
        >
          <template #default="{ row }">
            {{ formatDate(row.createdAt) }}
          </template>
        </el-table-column>
        
        <el-table-column
          label="操作"
          width="200"
          fixed="right"
        >
          <template #default="{ row }">
            <div class="action-buttons">
              <el-button 
                v-if="row.status === 'stopped'"
                type="success" 
                size="small"
                :loading="actionLoading[row.id]"
                @click="handleAction(row, 'start')"
              >
                启动
              </el-button>
              
              <el-button 
                v-if="row.status === 'running'"
                type="warning" 
                size="small"
                :loading="actionLoading[row.id]"
                @click="handleAction(row, 'stop')"
              >
                停止
              </el-button>
              
              <el-button 
                v-if="row.status === 'running'"
                type="info" 
                size="small"
                :loading="actionLoading[row.id]"
                @click="handleAction(row, 'restart')"
              >
                重启
              </el-button>
              
              <el-dropdown 
                :disabled="['creating', 'deleting'].includes(row.status)"
                @command="(command) => handleAction(row, command)"
              >
                <el-button
                  type="primary"
                  size="small"
                >
                  更多<el-icon class="el-icon--right">
                    <arrow-down />
                  </el-icon>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="console">
                      控制台
                    </el-dropdown-item>
                    <el-dropdown-item command="monitor">
                      监控
                    </el-dropdown-item>
                    <el-dropdown-item command="logs">
                      日志
                    </el-dropdown-item>
                    <el-dropdown-item 
                      command="delete" 
                      :disabled="row.status === 'running'"
                      divided
                    >
                      删除
                    </el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </div>
          </template>
        </el-table-column>
      </el-table>
      
      <!-- 分页 -->
      <div class="pagination-wrapper">
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.size"
          :page-sizes="[10, 20, 50]"
          :total="pagination.total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="loadInstances"
          @current-change="loadInstances"
        />
      </div>
    </el-card>

    <!-- 控制台对话框 -->
    <el-dialog
      v-model="consoleDialog.visible"
      :title="`${consoleDialog.instance?.name} - 控制台`"
      width="80%"
      top="5vh"
    >
      <div class="console-container">
        <div class="console-header">
          <el-button
            size="small"
            @click="clearConsole"
          >
            清空
          </el-button>
          <el-button
            size="small"
            @click="reconnectConsole"
          >
            重连
          </el-button>
        </div>
        <div
          ref="consoleRef"
          class="console-content"
        >
          <!-- 这里可以集成 xterm.js 或其他终端组件 -->
          <div class="console-placeholder">
            <p>控制台连接中...</p>
            <p>IP: {{ consoleDialog.instance?.ipAddress }}</p>
            <p>SSH端口: {{ consoleDialog.instance?.sshPort }}</p>
          </div>
        </div>
      </div>
    </el-dialog>

    <!-- 监控对话框 -->
    <el-dialog
      v-model="monitorDialog.visible"
      :title="`${monitorDialog.instance?.name} - 监控`"
      width="70%"
    >
      <div class="monitor-container">
        <!-- 这里可以集成监控图表 -->
        <div class="monitor-placeholder">
          <p>监控数据加载中...</p>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Box, ArrowDown } from '@element-plus/icons-vue'
import { getUserInstances, instanceAction } from '@/api/user'

const loading = ref(false)
const actionLoading = ref({})

const filterForm = reactive({
  status: '',
  region: '',
  instanceType: 'container'
})

const pagination = reactive({
  page: 1,
  size: 10,
  total: 0
})

const instances = ref([])

const consoleDialog = reactive({
  visible: false,
  instance: null
})

const monitorDialog = reactive({
  visible: false,
  instance: null
})

const consoleRef = ref()

// 获取状态类型
const getStatusType = (status) => {
  const types = {
    'running': 'success',
    'stopped': 'info',
    'creating': 'warning',
    'deleting': 'danger',
    'error': 'danger'
  }
  return types[status] || 'info'
}

// 获取状态文本
const getStatusText = (status) => {
  const texts = {
    'running': '运行中',
    'stopped': '已停止',
    'creating': '创建中',
    'deleting': '删除中',
    'error': '错误'
  }
  return texts[status] || status
}

// 格式化日期
const formatDate = (dateStr) => {
  return new Date(dateStr).toLocaleString('zh-CN')
}

// 复制到剪贴板
const copyToClipboard = async (text) => {
  if (!text) {
    ElMessage.warning('没有可复制的内容')
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      ElMessage.success('已复制到剪贴板')
      return
    }
    
    // 降级方案：使用传统的 document.execCommand
    const textArea = document.createElement('textarea')
    textArea.value = text
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

// 加载实例列表
const loadInstances = async (showSuccessMsg = false) => {
  loading.value = true
  try {
    const params = {
      page: pagination.page,
      size: pagination.size,
      ...filterForm
    }
    
    const response = await getUserInstances(params)
    instances.value = response.data.list
    pagination.total = response.data.total
    
    // 只有在明确刷新时才显示成功提示
    if (showSuccessMsg) {
      ElMessage.success(`已刷新，共 ${pagination.total} 个实例`)
    }
  } catch (error) {
    ElMessage.error('加载实例列表失败')
  } finally {
    loading.value = false
  }
}

// 重置筛选条件
const resetFilter = () => {
  filterForm.status = ''
  filterForm.region = ''
  pagination.page = 1
  loadInstances(true)
}

// 处理实例操作
const handleAction = async (instance, action) => {
  if (action === 'console') {
    consoleDialog.instance = instance
    consoleDialog.visible = true
    return
  }
  
  if (action === 'monitor') {
    monitorDialog.instance = instance
    monitorDialog.visible = true
    return
  }
  
  if (action === 'logs') {
    ElMessage.info('日志功能开发中...')
    return
  }
  
  if (action === 'delete') {
    try {
      await ElMessageBox.confirm(
        `确定要删除容器 "${instance.name}" 吗？此操作不可恢复。`,
        '确认删除',
        {
          confirmButtonText: '确定',
          cancelButtonText: '取消',
          type: 'warning',
        }
      )
    } catch {
      return
    }
  }
  
  actionLoading.value[instance.id] = true
  
  try {
    await instanceAction({
      instanceId: instance.id,
      action: action
    })
    
    const actionTexts = {
      'start': '启动',
      'stop': '停止',
      'restart': '重启',
      'delete': '删除'
    }
    
    ElMessage.success(`${actionTexts[action]}操作已提交`)
    
    setTimeout(() => {
      loadInstances()
    }, 1000)
    
  } catch (error) {
    ElMessage.error(error.response?.data?.message || '操作失败')
  } finally {
    actionLoading.value[instance.id] = false
  }
}

// 清空控制台
const clearConsole = () => {
}

// 重连控制台
const reconnectConsole = () => {
}

onMounted(() => {
  loadInstances()
})
</script>

<style scoped>
.user-containers {
  padding: 20px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.page-header h2 {
  margin: 0;
  color: #303133;
}

.header-actions {
  display: flex;
  gap: 10px;
}

.filter-card {
  margin-bottom: 20px;
}

.instances-card {
  min-height: 400px;
}

.instance-name {
  display: flex;
  align-items: center;
  gap: 8px;
}

.spec-info {
  font-size: 12px;
  color: #606266;
}

.text-muted {
  color: #c0c4cc;
}

.action-buttons {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
}

.pagination-wrapper {
  display: flex;
  justify-content: center;
  margin-top: 20px;
}

.console-container {
  height: 500px;
  display: flex;
  flex-direction: column;
}

.console-header {
  padding: 10px;
  border-bottom: 1px solid #ebeef5;
  display: flex;
  gap: 10px;
}

.console-content {
  flex: 1;
  background: #000;
  color: #fff;
  padding: 10px;
  font-family: 'Courier New', monospace;
  overflow-y: auto;
}

.console-placeholder {
  text-align: center;
  padding: 50px;
  color: #909399;
}

.monitor-container {
  height: 400px;
}

.monitor-placeholder {
  text-align: center;
  padding: 100px;
  color: #909399;
}

@media (max-width: 768px) {
  .user-containers {
    padding: 10px;
  }
  
  .page-header {
    flex-direction: column;
    gap: 15px;
    align-items: stretch;
  }
  
  .header-actions {
    justify-content: center;
  }
  
  .action-buttons {
    flex-direction: column;
  }
}
</style>
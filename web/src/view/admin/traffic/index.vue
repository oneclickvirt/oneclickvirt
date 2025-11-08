<template>
  <div class="admin-traffic">
    <div class="page-header">
      <h1>{{ $t('admin.traffic.title') }}</h1>
      <p>{{ $t('admin.traffic.subtitle') }}</p>
    </div>

    <!-- 系统流量概览 -->
    <div class="system-overview">
      <el-card>
        <template #header>
          <div class="card-header">
            <span>{{ $t('admin.traffic.systemOverview') }}</span>
            <div class="header-actions">
              <el-button
                size="small"
                :loading="overviewLoading"
                @click="loadSystemOverview"
              >
                <el-icon><Refresh /></el-icon>
                {{ $t('common.refresh') }}
              </el-button>
              <el-button
                size="small"
                type="primary"
                :loading="syncingAllTraffic"
                @click="syncAllTrafficData"
              >
                {{ $t('admin.traffic.syncAllTraffic') }}
              </el-button>
            </div>
          </div>
        </template>

        <div
          v-if="overviewLoading"
          class="loading-container"
        >
          <el-skeleton
            :rows="3"
            animated
          />
        </div>

        <div
          v-else-if="systemOverview"
          class="overview-content"
        >
          <el-row :gutter="20">
            <el-col :span="6">
              <div class="stat-card">
                <div class="stat-title">
                  {{ $t('admin.traffic.monthlyTotalTraffic') }}
                </div>
                <div class="stat-value">
                  {{ systemOverview.traffic?.formatted?.total_bytes || '0 B' }}
                </div>
                <div class="stat-subtitle">
                  {{ $t('admin.traffic.uplink') }}: {{ systemOverview.traffic?.formatted?.total_tx || '0 B' }} / 
                  {{ $t('admin.traffic.downlink') }}: {{ systemOverview.traffic?.formatted?.total_rx || '0 B' }}
                </div>
              </div>
            </el-col>
            <el-col :span="6">
              <div class="stat-card">
                <div class="stat-title">
                  {{ $t('admin.traffic.userStats') }}
                </div>
                <div class="stat-value">
                  {{ systemOverview.users?.total || 0 }}
                </div>
                <div class="stat-subtitle">
                  {{ $t('admin.traffic.limited') }}: {{ systemOverview.users?.limited || 0 }} 
                  ({{ (systemOverview.users?.limited_percent || 0).toFixed(1) }}%)
                </div>
              </div>
            </el-col>
            <el-col :span="6">
              <div class="stat-card">
                <div class="stat-title">
                  {{ $t('admin.traffic.providerStats') }}
                </div>
                <div class="stat-value">
                  {{ systemOverview.providers?.total || 0 }}
                </div>
                <div class="stat-subtitle">
                  {{ $t('admin.traffic.limited') }}: {{ systemOverview.providers?.limited || 0 }} 
                  ({{ (systemOverview.providers?.limited_percent || 0).toFixed(1) }}%)
                </div>
              </div>
            </el-col>
            <el-col :span="6">
              <div class="stat-card">
                <div class="stat-title">
                  {{ $t('admin.traffic.totalInstances') }}
                </div>
                <div class="stat-value">
                  {{ systemOverview.instances || 0 }}
                </div>
                <div class="stat-subtitle">
                  {{ $t('admin.traffic.activeInstanceStats') }}
                </div>
              </div>
            </el-col>
          </el-row>

          <div class="period-info">
            <el-text
              type="info"
              size="small"
            >
              <el-icon><Calendar /></el-icon>
              {{ $t('admin.traffic.statsPeriod') }}: {{ systemOverview.period }}
            </el-text>
          </div>
        </div>
      </el-card>
    </div>

    <!-- 流量排行榜 -->
    <div class="traffic-ranking">
      <el-card>
        <template #header>
          <div class="card-header">
            <span>{{ $t('admin.traffic.trafficRanking') }}</span>
          </div>
        </template>

        <!-- 搜索和批量操作工具栏 -->
        <div class="toolbar">
          <div class="search-section">
            <el-input
              v-model="searchParams.username"
              :placeholder="$t('admin.traffic.searchByUsername')"
              style="width: 200px;"
              clearable
              @keyup.enter="handleSearch"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
            <el-input
              v-model="searchParams.nickname"
              :placeholder="$t('admin.traffic.searchByNickname')"
              style="width: 200px; margin-left: 10px;"
              clearable
              @keyup.enter="handleSearch"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
            <el-button 
              type="primary" 
              style="margin-left: 10px;"
              @click="handleSearch"
            >
              {{ $t('common.search') }}
            </el-button>
            <el-button 
              @click="resetSearch"
            >
              {{ $t('common.reset') }}
            </el-button>
            <el-button
              size="default"
              :loading="rankingLoading"
              @click="loadTrafficRanking"
            >
              <el-icon><Refresh /></el-icon>
              {{ $t('common.refresh') }}
            </el-button>
          </div>

          <!-- 批量操作 -->
          <div
            v-if="selectedUsers.length > 0"
            class="batch-actions"
          >
            <span class="selection-info">
              {{ $t('admin.traffic.selected') }} {{ selectedUsers.length }} {{ $t('admin.traffic.users') }}
            </span>
            <el-button
              size="small"
              type="primary"
              @click="handleBatchSync"
            >
              {{ $t('admin.traffic.batchSync') }}
            </el-button>
            <el-button
              size="small"
              type="warning"
              @click="handleBatchLimit"
            >
              {{ $t('admin.traffic.batchLimit') }}
            </el-button>
            <el-button
              size="small"
              type="success"
              @click="handleBatchUnlimit"
            >
              {{ $t('admin.traffic.batchUnlimit') }}
            </el-button>
          </div>
        </div>

        <div
          v-if="rankingLoading"
          class="loading-container"
        >
          <el-skeleton
            :rows="5"
            animated
          />
        </div>

        <div v-else-if="trafficRanking && trafficRanking.length > 0">
          <el-table
            :data="trafficRanking"
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
              :label="$t('admin.traffic.rank')"
              width="80"
              align="center"
            >
              <template #default="{ row }">
                <el-tag 
                  :type="getRankTagType(row.rank)"
                  effect="dark"
                  size="small"
                >
                  #{{ row.rank }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column
              prop="username"
              :label="$t('admin.traffic.username')"
              width="150"
            />
            <el-table-column
              prop="nickname"
              :label="$t('admin.traffic.nickname')"
              width="150"
            />
            <el-table-column
              :label="$t('admin.traffic.monthlyUsage')"
              width="120"
            >
              <template #default="{ row }">
                {{ row.formatted?.month_usage || formatBytes(row.month_usage) }}
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('admin.traffic.totalLimit')"
              width="120"
            >
              <template #default="{ row }">
                {{ row.formatted?.total_limit || formatTrafficMB(row.total_limit) }}
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('admin.traffic.usageRate')"
              width="120"
              align="center"
            >
              <template #default="{ row }">
                <el-progress
                  :percentage="Math.min(row.usage_percent || 0, 100)"
                  :color="getUsageColor(row.usage_percent || 0)"
                  :stroke-width="8"
                  :show-text="false"
                />
                <div style="margin-top: 4px; font-size: 12px;">
                  {{ (row.usage_percent || 0).toFixed(1) }}%
                </div>
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('common.status')"
              width="100"
              align="center"
            >
              <template #default="{ row }">
                <el-tag 
                  :type="row.is_limited ? 'danger' : 'success'"
                  size="small"
                >
                  {{ row.is_limited ? $t('admin.traffic.limitedStatus') : $t('common.normal') }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('common.actions')"
              width="380"
              align="center"
            >
              <template #default="{ row }">
                <el-button
                  size="small"
                  @click="viewUserTraffic(row.user_id)"
                >
                  {{ $t('admin.traffic.viewDetails') }}
                </el-button>
                <el-button
                  size="small"
                  type="primary"
                  :loading="syncingUsers.includes(row.user_id)"
                  @click="syncUserTrafficData(row.user_id)"
                >
                  {{ $t('admin.traffic.syncTraffic') }}
                </el-button>
                <el-button
                  v-if="!row.is_limited"
                  size="small"
                  type="warning"
                  @click="limitUser(row)"
                >
                  {{ $t('admin.traffic.limitTraffic') }}
                </el-button>
                <el-button
                  v-else
                  size="small"
                  type="success"
                  @click="unlimitUser(row)"
                >
                  {{ $t('admin.traffic.removeLimit') }}
                </el-button>
                <el-button
                  size="small"
                  type="danger"
                  @click="clearUserTraffic(row)"
                >
                  {{ $t('admin.traffic.clearTraffic') }}
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
        </div>

        <div
          v-else
          class="empty-state"
        >
          <el-empty :description="$t('admin.traffic.noTrafficData')" />
        </div>
      </el-card>
    </div>

    <!-- 用户流量详情对话框 -->
    <el-dialog
      v-model="userTrafficDialogVisible"
      :title="$t('admin.traffic.userTrafficDetails')"
      width="600px"
    >
      <div
        v-if="userTrafficLoading"
        class="loading-container"
      >
        <el-skeleton
          :rows="4"
          animated
        />
      </div>

      <div
        v-else-if="selectedUserTraffic"
        class="user-traffic-detail"
      >
        <el-descriptions
          :column="2"
          border
        >
          <el-descriptions-item :label="$t('admin.traffic.userId')">
            {{ selectedUserTraffic.user_id }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.traffic.dataSource')">
            <el-tag type="success">
              {{ $t('admin.traffic.vnstatRealtime') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.traffic.monthlyUsage')">
            {{ selectedUserTraffic.formatted?.current_usage || formatTrafficMB(selectedUserTraffic.current_month_usage) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.traffic.totalLimit')">
            {{ selectedUserTraffic.formatted?.total_limit || formatTrafficMB(selectedUserTraffic.total_limit) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.traffic.usageRate')">
            {{ (selectedUserTraffic.usage_percent || 0).toFixed(2) }}%
          </el-descriptions-item>
          <el-descriptions-item :label="$t('common.status')">
            <el-tag :type="selectedUserTraffic.is_limited ? 'danger' : 'success'">
              {{ selectedUserTraffic.is_limited ? $t('admin.traffic.limitedStatus') : $t('common.normal') }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>

        <div
          v-if="selectedUserTraffic.reset_time"
          style="margin-top: 15px;"
        >
          <el-text
            type="info"
            size="small"
          >
            <el-icon><Clock /></el-icon>
            {{ $t('admin.traffic.trafficResetTime') }}: {{ formatDate(selectedUserTraffic.reset_time) }}
          </el-text>
        </div>
      </div>

      <template #footer>
        <span class="dialog-footer">
          <el-button 
            type="primary"
            :loading="syncingUserDetail"
            @click="syncUserTrafficFromDetail"
          >
            {{ $t('admin.traffic.syncNow') }}
          </el-button>
          <el-button @click="userTrafficDialogVisible = false">{{ $t('common.close') }}</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 流量限制对话框 -->
    <el-dialog
      v-model="limitDialogVisible"
      :title="limitAction === 'limit' ? $t('admin.traffic.limitUserTraffic') : $t('admin.traffic.removeLimitTitle')"
      width="400px"
    >
      <el-form
        ref="limitFormRef"
        :model="limitForm"
        :rules="limitFormRules"
        label-width="80px"
      >
        <el-form-item :label="$t('common.user')">
          <el-text>{{ selectedUser?.username }} ({{ selectedUser?.email }})</el-text>
        </el-form-item>
        <el-form-item
          v-if="limitAction === 'limit'"
          :label="$t('admin.traffic.limitReason')"
          prop="reason"
        >
          <el-input
            v-model="limitForm.reason"
            type="textarea"
            :rows="3"
            :placeholder="$t('admin.traffic.enterLimitReason')"
          />
        </el-form-item>
      </el-form>

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="limitDialogVisible = false">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="limitSubmitting"
            @click="submitLimitAction"
          >
            {{ $t('common.confirm') }}{{ limitAction === 'limit' ? $t('admin.traffic.limit') : $t('admin.traffic.remove') }}
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { 
  Refresh, 
  Calendar, 
  Clock,
  Search
} from '@element-plus/icons-vue'
import { 
  getSystemTrafficOverview,
  getAllUsersTrafficRank,
  getUserTrafficStats,
  manageTrafficLimits,
  batchManageTrafficLimits,
  batchSyncUserTraffic,
  syncUserTraffic,
  syncAllTraffic,
  clearUserTrafficRecords
} from '@/api/admin'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// 响应式数据
const overviewLoading = ref(false)
const systemOverview = ref(null)
const syncingAllTraffic = ref(false)

const rankingLoading = ref(false)
const trafficRanking = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const selectedUsers = ref([])

const searchParams = reactive({
  username: '',
  nickname: ''
})

const userTrafficDialogVisible = ref(false)
const userTrafficLoading = ref(false)
const selectedUserTraffic = ref(null)
const syncingUserDetail = ref(false)

const limitDialogVisible = ref(false)
const limitSubmitting = ref(false)
const limitAction = ref('limit') // 'limit' 或 'unlimit'
const selectedUser = ref(null)
const syncingUsers = ref([])

const limitForm = reactive({
  reason: ''
})

const limitFormRules = {
  reason: [
    { required: true, message: () => t('admin.traffic.enterLimitReason'), trigger: 'blur' },
    { min: 5, message: () => t('admin.traffic.limitReasonMinLength'), trigger: 'blur' }
  ]
}

// 加载系统流量概览
const loadSystemOverview = async () => {
  overviewLoading.value = true
  try {
    const response = await getSystemTrafficOverview()
    if (response.code === 0) {
      systemOverview.value = response.data
    } else {
      ElMessage.error(`${t('admin.traffic.loadOverviewFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('获取系统概览失败:', error)
    ElMessage.error(t('admin.traffic.loadOverviewError'))
  } finally {
    overviewLoading.value = false
  }
}

// 加载流量排行榜
const loadTrafficRanking = async () => {
  rankingLoading.value = true
  try {
    const params = {
      page: currentPage.value,
      pageSize: pageSize.value,
      username: searchParams.username || undefined,
      nickname: searchParams.nickname || undefined
    }
    const response = await getAllUsersTrafficRank(params)
    if (response.code === 0) {
      trafficRanking.value = response.data.rankings || []
      total.value = response.data.total || 0
    } else {
      ElMessage.error(`${t('admin.traffic.loadRankingFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('获取流量排行榜失败:', error)
    ElMessage.error(t('admin.traffic.loadRankingError'))
  } finally {
    rankingLoading.value = false
  }
}

// 搜索处理
const handleSearch = () => {
  currentPage.value = 1
  loadTrafficRanking()
}

// 重置搜索
const resetSearch = () => {
  searchParams.username = ''
  searchParams.nickname = ''
  currentPage.value = 1
  loadTrafficRanking()
}

// 分页处理
const handleSizeChange = (newSize) => {
  pageSize.value = newSize
  currentPage.value = 1
  loadTrafficRanking()
}

const handleCurrentChange = (newPage) => {
  currentPage.value = newPage
  loadTrafficRanking()
}

// 选择处理
const handleSelectionChange = (selection) => {
  selectedUsers.value = selection
}

// 批量同步
const handleBatchSync = async () => {
  if (selectedUsers.value.length === 0) {
    ElMessage.warning(t('admin.traffic.pleaseSelectUsers'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('admin.traffic.confirmBatchSync', { count: selectedUsers.value.length }),
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )

    const userIds = selectedUsers.value.map(user => user.user_id)
    const response = await batchSyncUserTraffic({ user_ids: userIds })
    
    if (response.code === 0) {
      ElMessage.success(t('admin.traffic.batchSyncSuccess'))
      setTimeout(() => {
        loadTrafficRanking()
      }, 3000)
    } else {
      ElMessage.error(`${t('admin.traffic.batchSyncFailed')}: ${response.msg}`)
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('批量同步失败:', error)
      ElMessage.error(t('admin.traffic.batchSyncError'))
    }
  }
}

// 批量限制
const handleBatchLimit = async () => {
  if (selectedUsers.value.length === 0) {
    ElMessage.warning(t('admin.traffic.pleaseSelectUsers'))
    return
  }

  try {
    const { value: reason } = await ElMessageBox.prompt(
      t('admin.traffic.enterLimitReason'),
      t('admin.traffic.batchLimit'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        inputPattern: /.{5,}/,
        inputErrorMessage: t('admin.traffic.limitReasonMinLength')
      }
    )

    const userIds = selectedUsers.value.map(user => user.user_id)
    const response = await batchManageTrafficLimits({
      action: 'limit',
      user_ids: userIds,
      reason: reason
    })
    
    if (response.code === 0) {
      ElMessage.success(response.msg)
      loadTrafficRanking()
    } else {
      ElMessage.error(`${t('admin.traffic.batchLimitFailed')}: ${response.msg}`)
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('批量限制失败:', error)
      ElMessage.error(t('admin.traffic.batchLimitError'))
    }
  }
}

// 批量解除限制
const handleBatchUnlimit = async () => {
  if (selectedUsers.value.length === 0) {
    ElMessage.warning(t('admin.traffic.pleaseSelectUsers'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('admin.traffic.confirmBatchUnlimit', { count: selectedUsers.value.length }),
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )

    const userIds = selectedUsers.value.map(user => user.user_id)
    const response = await batchManageTrafficLimits({
      action: 'unlimit',
      user_ids: userIds
    })
    
    if (response.code === 0) {
      ElMessage.success(response.msg)
      loadTrafficRanking()
    } else {
      ElMessage.error(`${t('admin.traffic.batchUnlimitFailed')}: ${response.msg}`)
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('批量解除限制失败:', error)
      ElMessage.error(t('admin.traffic.batchUnlimitError'))
    }
  }
}

// 查看用户流量详情
const viewUserTraffic = async (userId) => {
  userTrafficLoading.value = true
  userTrafficDialogVisible.value = true
  try {
    const response = await getUserTrafficStats(userId)
    if (response.code === 0) {
      selectedUserTraffic.value = response.data
    } else {
      ElMessage.error(`${t('admin.traffic.loadUserDetailsFailed')}: ${response.msg}`)
      userTrafficDialogVisible.value = false
    }
  } catch (error) {
    console.error('获取用户流量详情失败:', error)
    ElMessage.error(t('admin.traffic.loadUserDetailsError'))
    userTrafficDialogVisible.value = false
  } finally {
    userTrafficLoading.value = false
  }
}

// 限制用户流量
const limitUser = (user) => {
  selectedUser.value = user
  limitAction.value = 'limit'
  limitForm.reason = ''
  limitDialogVisible.value = true
}

// 解除用户流量限制
const unlimitUser = (user) => {
  selectedUser.value = user
  limitAction.value = 'unlimit'
  limitDialogVisible.value = true
}

// 提交流量限制操作
const submitLimitAction = async () => {
  if (limitAction.value === 'limit') {
    // 验证表单
    if (!limitForm.reason.trim()) {
      ElMessage.error(t('admin.traffic.enterLimitReason'))
      return
    }
  }

  limitSubmitting.value = true
  try {
    const data = {
      type: 'user',
      action: limitAction.value,
      target_id: selectedUser.value.user_id,
      reason: limitForm.reason
    }

    const response = await manageTrafficLimits(data)
    if (response.code === 0) {
      ElMessage.success(t('admin.traffic.limitActionSuccess', { action: limitAction.value === 'limit' ? t('admin.traffic.limit') : t('admin.traffic.remove') }))
      limitDialogVisible.value = false
      
      // 更新列表中的状态
      const userIndex = trafficRanking.value.findIndex(u => u.user_id === selectedUser.value.user_id)
      if (userIndex !== -1) {
        trafficRanking.value[userIndex].is_limited = limitAction.value === 'limit'
      }
    } else {
      ElMessage.error(`${t('message.operationFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('操作失败:', error)
    ElMessage.error(t('admin.traffic.operationError'))
  } finally {
    limitSubmitting.value = false
  }
}

// 同步用户流量
const syncUserTrafficData = async (userId) => {
  // 防止重复点击
  if (syncingUsers.value.includes(userId)) {
    return
  }

  syncingUsers.value.push(userId)
  try {
    const response = await syncUserTraffic(userId)
    if (response.code === 0) {
      ElMessage.success(t('admin.traffic.syncTriggered'))
      
      // 3秒后刷新排行榜数据
      setTimeout(() => {
        loadTrafficRanking()
      }, 3000)
    } else {
      ElMessage.error(`${t('admin.traffic.syncFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('同步用户流量失败:', error)
    ElMessage.error(t('admin.traffic.syncError'))
  } finally {
    // 从同步中列表移除
    const index = syncingUsers.value.indexOf(userId)
    if (index > -1) {
      syncingUsers.value.splice(index, 1)
    }
  }
}

// 从详情弹窗同步流量
const syncUserTrafficFromDetail = async () => {
  if (!selectedUserTraffic.value || syncingUserDetail.value) {
    return
  }

  syncingUserDetail.value = true
  try {
    const response = await syncUserTraffic(selectedUserTraffic.value.user_id)
    if (response.code === 0) {
      ElMessage.success(t('admin.traffic.syncTriggered'))
      
      // 3秒后重新获取用户详情
      setTimeout(async () => {
        await viewUserTraffic(selectedUserTraffic.value.user_id)
        loadTrafficRanking() // 同时刷新列表
      }, 3000)
    } else {
      ElMessage.error(`${t('admin.traffic.syncFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('同步用户流量失败:', error)
    ElMessage.error(t('admin.traffic.syncError'))
  } finally {
    syncingUserDetail.value = false
  }
}

// 同步全部流量
const syncAllTrafficData = async () => {
  syncingAllTraffic.value = true
  try {
    const response = await syncAllTraffic()
    if (response.code === 0) {
      ElMessage.success(t('admin.traffic.syncAllTriggered'))
      
      // 5秒后刷新概览和排行榜数据
      setTimeout(() => {
        loadSystemOverview()
        loadTrafficRanking()
      }, 5000)
    } else {
      ElMessage.error(`${t('admin.traffic.syncFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('同步全部流量失败:', error)
    ElMessage.error(t('admin.traffic.syncError'))
  } finally {
    syncingAllTraffic.value = false
  }
}

// 清空用户流量记录
const clearUserTraffic = async (user) => {
  try {
    await ElMessageBox.confirm(
      t('admin.traffic.clearTrafficConfirm', { username: user.username }),
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true
      }
    )

    const response = await clearUserTrafficRecords(user.user_id)
    if (response.code === 0) {
      ElMessage.success(t('admin.traffic.clearTrafficSuccess', { 
        username: user.username, 
        count: response.data.deleted_count 
      }))
      
      // 刷新列表
      loadTrafficRanking()
      loadSystemOverview()
    } else {
      ElMessage.error(`${t('admin.traffic.clearTrafficFailed')}: ${response.msg}`)
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('清空用户流量记录失败:', error)
      ElMessage.error(t('admin.traffic.clearTrafficError'))
    }
  }
}

// 工具函数
const formatBytes = (bytes) => {
  if (!bytes || bytes === 0) return '0 B'
  
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = bytes
  let unitIndex = 0
  
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex++
  }
  
  return `${size.toFixed(2)} ${units[unitIndex]}`
}

// 格式化MB单位的流量数据
const formatTrafficMB = (mb) => {
  if (!mb || mb === 0) return '0 B'
  
  const GB_IN_MB = 1024
  const TB_IN_MB = 1024 * 1024
  
  if (mb >= TB_IN_MB) {
    return `${(mb / TB_IN_MB).toFixed(2)} TB`
  } else if (mb >= GB_IN_MB) {
    return `${(mb / GB_IN_MB).toFixed(2)} GB`
  } else if (mb >= 1) {
    return `${mb.toFixed(2)} MB`
  } else if (mb > 0) {
    return `${(mb * 1024).toFixed(2)} KB`
  }
  return '0 B'
}

const formatDate = (dateString) => {
  if (!dateString) return '未设置'
  return new Date(dateString).toLocaleString('zh-CN')
}

const getRankTagType = (rank) => {
  if (rank === 1) return 'danger'
  if (rank <= 3) return 'warning'
  if (rank <= 10) return 'primary'
  return 'info'
}

const getUsageColor = (percentage) => {
  if (percentage < 60) return '#67c23a'
  if (percentage < 80) return '#e6a23c'
  return '#f56c6c'
}

// 页面加载时获取数据
onMounted(() => {
  loadSystemOverview()
  loadTrafficRanking()
})
</script>

<style scoped>
.admin-traffic {
  margin: -24px -24px -24px -24px;
  padding: 24px 0 24px 24px;
  width: calc(100% + 48px);
}

.page-header {
  margin-bottom: 20px;
  padding-right: 24px;
}

.page-header h1 {
  margin: 0 0 8px 0;
  color: var(--el-text-color-primary);
}

.page-header p {
  margin: 0;
  color: var(--el-text-color-regular);
}

.system-overview {
  margin-bottom: 20px;
  padding-right: 24px;
}

.traffic-ranking {
  padding-right: 0;
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

.header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.loading-container {
  padding: 20px;
}

.overview-content {
  padding: 10px 0;
}

.stat-card {
  text-align: center;
  padding: 20px;
  background: var(--el-fill-color-lighter);
  border-radius: 8px;
  border: 1px solid var(--el-border-color-light);
}

.stat-title {
  font-size: 14px;
  color: var(--el-text-color-secondary);
  margin-bottom: 10px;
}

.stat-value {
  font-size: 24px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  margin-bottom: 8px;
  font-family: monospace;
}

.stat-subtitle {
  font-size: 12px;
  color: var(--el-text-color-regular);
}

.period-info {
  text-align: center;
  margin-top: 20px;
}

.traffic-ranking {
  margin-bottom: 20px;
  padding-right: 0;
}

.traffic-ranking :deep(.el-card) {
  border-radius: 0;
  margin-right: 0;
}

.empty-state {
  padding: 40px;
  text-align: center;
}

.user-traffic-detail {
  padding: 10px 0;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.toolbar {
  margin-bottom: 16px;
}

.search-section {
  display: flex;
  align-items: center;
  margin-bottom: 12px;
}

.batch-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px;
  background-color: #f0f9ff;
  border: 1px solid #b3e0ff;
  border-radius: 4px;
}

.selection-info {
  font-size: 14px;
  color: #409eff;
  font-weight: 500;
  margin-right: 10px;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}
</style>

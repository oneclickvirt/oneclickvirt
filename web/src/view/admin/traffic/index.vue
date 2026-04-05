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
                {{ row.formatted?.month_usage || formatTrafficMB(row.month_usage) }}
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
              width="450"
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
              {{ $t('admin.traffic.pmacctRealtime') }}
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
import { onMounted } from 'vue'
import { Refresh, Calendar, Clock, Search } from '@element-plus/icons-vue'
import { useTrafficManagement } from './composables/useTrafficManagement'

const {
  overviewLoading, systemOverview, syncingAllTraffic,
  rankingLoading, trafficRanking, currentPage, pageSize, total, selectedUsers,
  searchParams,
  userTrafficDialogVisible, userTrafficLoading, selectedUserTraffic, syncingUserDetail,
  limitDialogVisible, limitSubmitting, limitAction, selectedUser, syncingUsers,
  limitForm, limitFormRules,
  loadSystemOverview, loadTrafficRanking,
  handleSearch, resetSearch, handleSizeChange, handleCurrentChange, handleSelectionChange,
  handleBatchSync, handleBatchLimit, handleBatchUnlimit,
  viewUserTraffic, limitUser, unlimitUser, submitLimitAction,
  syncUserTrafficData, syncUserTrafficFromDetail, syncAllTrafficData,
  clearUserTraffic,
  formatBytes, formatTrafficMB, formatDate, getRankTagType, getUsageColor,
  t
} = useTrafficManagement()

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
    color: var(--text-color-primary);
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
  background-color: var(--info-bg);
  border: 1px solid rgba(22, 163, 74, 0.2);
  border-radius: 4px;
}

.selection-info {
  font-size: 14px;
  color: #16a34a;
  font-weight: 500;
  margin-right: 10px;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}
</style>

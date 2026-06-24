<template>
  <div class="user-instances">
    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-container"
    >
      <el-loading-directive />
      <div class="loading-text">
        {{ t('user.instances.loadingInstances') }}
      </div>
    </div>
    
    <!-- 主要内容 -->
    <div v-else>
      <div class="page-header">
        <h1>{{ t('user.instances.title') }}</h1>
        <p>{{ t('user.instances.subtitle') }}</p>
      </div>

      <!-- 筛选和搜索 -->
      <div class="filter-section">
        <el-form
          :inline="true"
          :model="filterForm"
        >
          <el-form-item>
            <el-input
              v-model="filterForm.name"
              :placeholder="t('user.instances.searchByName')"
              clearable
              style="width: 200px;"
            />
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="filterForm.providerName"
              :placeholder="t('user.instances.searchByProvider')"
              clearable
              style="width: 200px;"
            />
          </el-form-item>
          <el-form-item>
            <el-select
              v-model="filterForm.type"
              :placeholder="t('user.instances.selectType')"
              clearable
              style="width: 150px;"
            >
              <el-option
                :label="t('user.instances.allTypes')"
                value=""
              />
              <el-option
                :label="t('user.instances.vm')"
                value="vm"
              />
              <el-option
                :label="t('user.instances.container')"
                value="container"
              />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-select
              v-model="filterForm.status"
              :placeholder="t('user.instances.selectStatus')"
              clearable
              style="width: 150px;"
            >
              <el-option
                :label="t('user.instances.allStatuses')"
                value=""
              />
              <el-option
                :label="t('user.instances.statusRunning')"
                value="running"
              />
              <el-option
                :label="t('user.instances.statusStopped')"
                value="stopped"
              />
              <el-option
                :label="t('user.instances.statusPaused')"
                value="paused"
              />
              <el-option
                :label="t('user.instances.statusCreating')"
                value="creating"
              />
              <el-option
                :label="t('user.instances.statusStarting')"
                value="starting"
              />
              <el-option
                :label="t('user.instances.statusStopping')"
                value="stopping"
              />
              <el-option
                :label="t('user.instances.statusRestarting')"
                value="restarting"
              />
              <el-option
                :label="t('user.instances.statusRebuilding')"
                value="rebuilding"
              />
              <el-option
                :label="t('user.instances.statusResetting')"
                value="resetting"
              />
              <el-option
                :label="t('user.instances.statusError')"
                value="error"
              />
              <el-option
                :label="t('user.instances.statusFailed')"
                value="failed"
              />
              <el-option
                :label="t('user.instances.statusDeleting')"
                value="deleting"
              />
              <el-option
                :label="t('user.instances.statusDeleted')"
                value="deleted"
              />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              @click="handleSearch"
            >
              {{ t('user.instances.search') }}
            </el-button>
            <el-button @click="resetFilter">
              {{ t('user.instances.reset') }}
            </el-button>
          </el-form-item>
        </el-form>
      </div>

      <!-- 实例列表 -->
      <div class="instances-grid">
        <div 
          v-for="instance in instances" 
          :key="instance.id" 
          class="instance-card"
          @click="viewInstanceDetail(instance)"
        >
          <div class="instance-header">
            <div class="instance-info">
              <h3>{{ instance.name }}</h3>
              <div class="instance-type">
                <el-tag :type="instance.instance_type === 'vm' ? 'primary' : 'success'">
                  {{ instance.instance_type === 'vm' ? t('user.instances.vm') : t('user.instances.container') }}
                </el-tag>
                <el-tag 
                  v-if="instance.providerType"
                  :type="getProviderTypeColor(instance.providerType)"
                  style="margin-left: 8px;"
                >
                  {{ getProviderTypeName(instance.providerType) }}
                </el-tag>
              </div>
            </div>
            <div class="instance-status">
              <el-tag 
                :type="getStatusType(instance.status)"
                effect="dark"
              >
                {{ getStatusText(instance.status) }}
              </el-tag>
            </div>
          </div>

          <div class="instance-details">
            <div class="detail-item">
              <span class="label">{{ t('user.instances.configuration') }}:</span>
              <span class="value">{{ instance.cpu }}{{ t('user.instances.cores') }} / {{ formatMemorySize(instance.memory) }} / {{ formatDiskSize(instance.disk) }}</span>
            </div>
            <div class="detail-item">
              <span class="label">{{ t('user.instances.bandwidth') }}:</span>
              <span class="value">{{ instance.bandwidth || 100 }}Mbps</span>
            </div>
            <div class="detail-item">
              <span class="label">{{ t('user.instances.system') }}:</span>
              <span class="value"><OsIcon
                :name="instance.osType || instance.image"
                :size="18"
                style="margin-right: 4px;"
              />{{ instance.osType }}</span>
            </div>
            <div class="detail-item">
              <span class="label">{{ t('user.instances.createdAt') }}:</span>
              <span class="value">{{ formatDate(instance.createdAt) }}</span>
            </div>
            <!-- 端口映射信息 -->
            <div
              v-if="instance.portMappings && instance.portMappings.length > 0"
              class="detail-item port-info"
            >
              <span class="label">{{ t('user.instances.portMapping') }}:</span>
              <div class="port-mappings">
                <div class="public-ip">
                  <el-tag
                    type="info"
                    size="small"
                  >
                    {{ t('user.instances.publicIP') }}: {{ instance.publicIP || t('user.instances.unassigned') }}
                  </el-tag>
                </div>
                <!-- 普通用户不显示端口区间 -->
                <div class="port-list">
                  <el-tag 
                    v-for="port in instance.portMappings.slice(0, 3)" 
                    :key="port.id"
                    size="small"
                    effect="plain"
                    class="port-tag"
                    :type="port.isSSH ? 'warning' : 'primary'"
                  >
                    <span v-if="port.isSSH">SSH: {{ port.hostPort }}</span>
                    <span v-else>{{ port.hostPort }}:{{ port.guestPort }}/{{ port.protocol }}</span>
                  </el-tag>
                  <el-tag 
                    v-if="instance.portMappings.length > 3"
                    size="small"
                    type="info"
                    effect="plain"
                  >
                    {{ t('user.instances.morePortsCount', { count: instance.portMappings.length - 3 }) }}
                  </el-tag>
                </div>
              </div>
            </div>
          </div>

          <!-- 实例操作按钮 -->
          <div
            class="instance-actions"
            @click.stop
          >
            <el-button
              v-if="instance.trafficQuotaVisible !== false"
              size="small"
              type="primary"
              @click="showTrafficDetail(instance)"
            >
              <el-icon><TrendCharts /></el-icon>
              {{ t('user.instances.trafficDetail') }}
            </el-button>
            <el-button
              size="small"
              @click="viewInstanceDetail(instance)"
            >
              <el-icon><View /></el-icon>
              {{ t('user.instances.viewDetail') }}
            </el-button>
            <el-button
              size="small"
              type="success"
              :disabled="instance.trafficOperationLocked"
              :title="instance.trafficOperationLockMessage || ''"
              @click="createShareLink(instance)"
            >
              <el-icon><Link /></el-icon>
              {{ t('user.instances.share') }}
            </el-button>
          </div>
        </div>
      </div>

      <!-- 空状态 -->
      <el-empty
        v-if="instances.length === 0 && !loading"
        :description="t('user.instances.noInstances')"
      >
        <el-button
          type="primary"
          @click="$router.push('/user/apply')"
        >
          {{ t('user.instances.applyNow') }}
        </el-button>
      </el-empty>

      <!-- 分页 -->
      <div
        v-if="total > 0"
        class="pagination"
      >
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.pageSize"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="() => loadInstances()"
          @current-change="() => loadInstances()"
        />
      </div>

      <!-- 加载状态 -->
      <div
        v-if="loading"
        class="loading-container"
      >
        <el-skeleton
          :rows="5"
          animated
        />
      </div>
    </div> <!-- 结束主要内容区域 -->

    <!-- 流量详情对话框 -->
    <InstanceTrafficDetail
      v-model="showTrafficDialog"
      :instance-id="selectedInstanceForTraffic?.id"
      :instance-name="selectedInstanceForTraffic?.name"
    />
  </div>
</template>

<script setup>
import { TrendCharts, View, Link } from '@element-plus/icons-vue'
import { formatDiskSize, formatMemorySize } from '@/utils/unit-formatter'
import InstanceTrafficDetail from '@/components/InstanceTrafficDetail.vue'
import OsIcon from '@/components/OsIcon.vue'
import { useUserInstances } from './composables/useUserInstances.js'
import { useI18n } from 'vue-i18n'
const { t } = useI18n()

const {
  loading,
  instances,
  total,
  showTrafficDialog,
  selectedInstanceForTraffic,
  filterForm,
  pagination,
  handleSearch,
  loadInstances,
  resetFilter,
  getStatusType,
  getStatusText,
  getProviderTypeName,
  getProviderTypeColor,
  formatDate,
  viewInstanceDetail,
  showTrafficDetail,
  createShareLink
} = useUserInstances()
</script>

<style scoped>
.user-instances {
  padding: 24px;
}

.loading-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 400px;
  color: #666;
}

.loading-text {
  margin-top: 16px;
  font-size: 14px;
}

.page-header {
  margin-bottom: 24px;
}

.page-header h1 {
  margin: 0 0 8px 0;
  font-size: 24px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.page-header p {
  margin: 0;
  color: var(--text-color-secondary);
}

.filter-section {
  background: var(--card-bg-solid);
  padding: 16px;
  border-radius: 8px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  margin-bottom: 24px;
}

.instances-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(400px, 1fr));
  gap: 20px;
  margin-bottom: 24px;
}

.instance-card {
  background: var(--card-bg-solid);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  padding: 20px;
  cursor: pointer;
  transition: all 0.3s ease;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.instance-card:hover {
  border-color: #10b981;
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.15);
  transform: translateY(-2px);
}

.instance-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
}

.instance-info h3 {
  margin: 0 0 8px 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.instance-details {
  margin-bottom: 16px;
}

.detail-item {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 8px;
  font-size: 14px;
}

.detail-item.port-info {
  flex-direction: column;
  align-items: flex-start;
}

.detail-item .label {
  color: var(--text-color-secondary);
  font-weight: 500;
  min-width: 80px;
}

.detail-item .value {
  color: var(--text-color-primary);
  text-align: right;
  flex: 1;
}

.port-mappings {
  margin-top: 8px;
  width: 100%;
}

.public-ip, .port-range, .ipv6-info {
  margin-bottom: 8px;
}

.port-list {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 4px;
}

.port-tag {
  margin: 2px;
  font-size: 12px;
}

.instance-details {
  margin-bottom: 16px;
}

.detail-item {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 8px;
  font-size: 14px;
}

.detail-item.port-info {
  flex-direction: column;
  align-items: flex-start;
}

.detail-item .label {
  color: var(--text-color-secondary);
  font-weight: 500;
  min-width: 80px;
}

.detail-item .value {
  color: var(--text-color-primary);
  text-align: right;
  flex: 1;
}

.port-mappings {
  margin-top: 8px;
  width: 100%;
}

.public-ip, .port-range, .ipv6-info {
  margin-bottom: 8px;
}


.pagination {
  display: flex;
  justify-content: center;
  margin-top: 24px;
}

.loading-container {
  padding: 24px;
}

.instance-actions {
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: 12px;
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}

.instance-actions .el-button {
  font-size: 12px;
}
</style>

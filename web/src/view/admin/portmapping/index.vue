<template>
  <div>
    <el-card class="box-card">
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.portMapping.title') }}</span>
          <div class="header-actions">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              style="margin-right: 10px;"
            >
              <template #title>
                <span style="font-size: 12px;">
                  {{ $t('admin.portMapping.rangePortInfo') }}
                </span>
              </template>
            </el-alert>
            <el-button
              type="primary"
              @click="openAddDialog"
            >
              <el-icon><Plus /></el-icon>
              {{ $t('admin.portMapping.addManualPort') }}
            </el-button>
            <el-button
              v-if="selectedPortMappings.length > 0"
              type="danger"
              @click="batchDeleteDirect"
            >
              {{ $t('admin.portMapping.batchDelete') }} ({{ selectedPortMappings.length }})
            </el-button>
            <el-tooltip
              :content="$t('admin.portMapping.syncPortMappingsTooltip')"
              placement="bottom"
            >
              <el-button
                type="warning"
                @click="handleSyncPortMappings"
              >
                <el-icon><RefreshRight /></el-icon>
                {{ $t('admin.portMapping.syncPortMappings') }}
              </el-button>
            </el-tooltip>
          </div>
        </div>
      </template>
      
      <!-- 搜索和筛选 -->
      <div class="search-bar">
        <el-row :gutter="12">
          <el-col :span="5">
            <el-input 
              v-model="searchForm.keyword" 
              :placeholder="$t('admin.portMapping.searchInstance')"
              clearable
              @keyup.enter="searchPortMappings"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.providerId"
              :placeholder="$t('admin.portMapping.selectProvider')"
              clearable
              style="width: 100%;"
            >
              <el-option
                v-for="provider in providers"
                :key="provider.id"
                :label="provider.name"
                :value="provider.id"
              />
            </el-select>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.protocol"
              :placeholder="$t('admin.portMapping.protocol')"
              clearable
              style="width: 100%;"
            >
              <el-option
                :label="$t('admin.portMapping.protocolTCP')"
                value="tcp"
              />
              <el-option
                :label="$t('admin.portMapping.protocolUDP')"
                value="udp"
              />
              <el-option
                :label="$t('admin.portMapping.protocolBoth')"
                value="both"
              />
            </el-select>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.status"
              :placeholder="$t('common.status')"
              clearable
              style="width: 100%;"
            >
              <el-option
                :label="$t('admin.portMapping.statusActive')"
                value="active"
              />
              <el-option
                :label="$t('admin.portMapping.statusInactive')"
                value="inactive"
              />
            </el-select>
          </el-col>
          <el-col :span="7">
            <el-button
              type="primary"
              @click="searchPortMappings"
            >
              {{ $t('common.search') }}
            </el-button>
            <el-button @click="resetSearch">
              {{ $t('common.reset') }}
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
          :selectable="isDeletablePort"
        />
        <el-table-column
          prop="id"
          :label="$t('admin.portMapping.labelId')"
          width="80"
        />
        <el-table-column
          prop="portType"
          :label="$t('admin.portMapping.portType')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag :type="row.portType === 'manual' ? 'warning' : row.portType === 'batch' ? 'info' : 'success'">
              {{ row.portType === 'manual' ? $t('admin.portMapping.manualPort') : row.portType === 'batch' ? $t('admin.portMapping.batchPort') : $t('admin.portMapping.rangePort') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="mappingType"
          :label="$t('admin.portMapping.mappingMode')"
          width="150"
        >
          <template #default="{ row }">
            <el-tag
              :type="row.mappingType === 'controller' ? 'warning' : 'primary'"
              size="small"
            >
              {{ row.mappingType === 'controller' ? $t('admin.portMapping.mappingModeController') : $t('admin.portMapping.mappingModeNode') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="instanceName"
          :label="$t('admin.portMapping.instanceName')"
          width="150"
        />
        <el-table-column
          prop="providerName"
          :label="$t('admin.portMapping.provider')"
          width="120"
        />
        <el-table-column
          prop="publicIP"
          :label="$t('admin.portMapping.publicIP')"
          width="120"
        />
        <el-table-column
          :label="$t('admin.portMapping.publicPort')"
          width="140"
        >
          <template #default="{ row }">
            <span v-if="row.portType === 'batch' && row.portCount && row.portCount > 1">
              {{ row.hostPort }}-{{ row.hostPortEnd || (row.hostPort + row.portCount - 1) }}
              <el-tag
                size="small"
                type="info"
                style="margin-left: 5px;"
              >×{{ row.portCount }}</el-tag>
            </span>
            <span v-else>{{ row.hostPort }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.portMapping.internalPort')"
          width="140"
        >
          <template #default="{ row }">
            <span v-if="row.portType === 'batch' && row.portCount && row.portCount > 1">
              {{ row.guestPort }}-{{ row.guestPortEnd || (row.guestPort + row.portCount - 1) }}
            </span>
            <span v-else>{{ row.guestPort }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="protocol"
          :label="$t('admin.portMapping.protocol')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag
              v-if="row.protocol === 'both'"
              type="info"
              size="small"
            >
              {{ $t('admin.portMapping.protocolBoth') }}
            </el-tag>
            <el-tag
              v-else-if="row.protocol === 'tcp'"
              type="success"
              size="small"
            >
              {{ $t('admin.portMapping.protocolTCP') }}
            </el-tag>
            <el-tag
              v-else-if="row.protocol === 'udp'"
              type="warning"
              size="small"
            >
              {{ $t('admin.portMapping.protocolUDP') }}
            </el-tag>
            <span v-else>{{ row.protocol }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="$t('common.description')"
          width="120"
        />
        <el-table-column
          prop="isIPv6"
          :label="$t('admin.portMapping.labelIPv6')"
          width="80"
        >
          <template #default="{ row }">
            <el-tag :type="row.isIPv6 ? 'success' : 'info'">
              {{ row.isIPv6 ? $t('common.yes') : $t('common.no') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          :label="$t('common.status')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag 
              v-if="row.status === 'active'" 
              type="success"
            >
              {{ $t('admin.portMapping.statusActive') }}
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'creating' || row.status === 'pending'" 
              type="warning"
            >
              <el-icon class="is-loading">
                <Loading />
              </el-icon>
              {{ row.status === 'creating' ? $t('admin.portMapping.statusCreating') : $t('admin.portMapping.statusPending') }}
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'deleting'" 
              type="warning"
            >
              <el-icon class="is-loading">
                <Loading />
              </el-icon>
              {{ $t('admin.portMapping.statusDeleting') }}
            </el-tag>
            <el-tag 
              v-else-if="row.status === 'failed'" 
              type="danger"
            >
              {{ $t('admin.portMapping.statusFailed') }}
            </el-tag>
            <el-tag 
              v-else 
              type="info"
            >
              {{ row.status || $t('common.unknown') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="$t('common.createTime')"
          width="150"
        >
          <template #default="{ row }">
            {{ formatTime(row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="120"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button
              v-if="row.portType === 'manual' || row.portType === 'batch'"
              type="danger"
              size="small"
              @click="deletePortMappingHandler(row.id)"
            >
              {{ $t('common.delete') }}
            </el-button>
            <el-tooltip
              v-else
              :content="$t('admin.portMapping.rangePortNotDeletable')"
              placement="top"
            >
              <el-button
                type="info"
                size="small"
                disabled
              >
                {{ $t('admin.portMapping.notDeletable') }}
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
      :title="$t('admin.portMapping.addPortDialog')"
      width="600px"
      :before-close="handleAddDialogClose"
    >
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      >
        <template #title>
          <span style="font-size: 13px;">
            {{ portMappingHint }}
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
          :label="$t('admin.portMapping.selectInstance')"
          prop="instanceId"
        >
          <el-select
            v-model="addForm.instanceId"
            :placeholder="$t('admin.portMapping.searchInstancePlaceholder')"
            filterable
            clearable
            style="width: 100%"
            :filter-method="filterInstances"
            :no-data-text="instances.length === 0 ? $t('admin.portMapping.noInstanceData') : $t('admin.portMapping.noMatchingInstance')"
            popper-class="instance-select-dropdown"
            @change="onInstanceChange"
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
                  <span style="color: #909399; font-size: 12px; margin-left: 8px;">ID: {{ instance.id }}</span>
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
            <span v-if="filteredInstancesCount > 0">
              {{ $t('admin.portMapping.totalInstancesFound') }} <strong>{{ filteredInstancesCount }}</strong> {{ $t('admin.portMapping.availableInstances') }}
              <span v-if="filteredInstancesCount > 10">{{ $t('admin.portMapping.showingFirst10') }}</span>
            </span>
            <span
              v-else-if="supportedInstances.length === 0 && instances.length > 0"
              style="color: #e6a23c;"
            >
              ⚠️ {{ $t('admin.portMapping.noSupportedInstances') }}（{{ $t('admin.portMapping.instancesLoadedButNotSupported', { count: instances.length }) }}）
            </span>
            <span
              v-else
              style="color: #909399;"
            >
              {{ $t('admin.portMapping.pleaseSelectInstance') }}
            </span>
          </div>
          <div
            v-if="selectedInstanceProvider !== '-'"
            style="color: #67c23a; font-size: 12px; margin-top: 3px;"
          >
            {{ $t('admin.portMapping.currentInstanceProvider') }}: <strong>{{ selectedInstanceProvider }}</strong>
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.internalPort')"
          prop="guestPort"
        >
          <el-input-number
            v-model="addForm.guestPort"
            :min="1"
            :max="65535"
            :controls="false"
            :placeholder="$t('admin.portMapping.internalPortPlaceholder')"
            style="width: 100%"
            @change="updatePortRange"
          />
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.portCount')"
          prop="portCount"
        >
          <el-input-number
            v-model="addForm.portCount"
            :min="1"
            :max="100"
            :controls="true"
            :placeholder="$t('admin.portMapping.portCountPlaceholder')"
            style="width: 100%"
            @change="updatePortRange"
          />
          <div style="color: #909399; font-size: 12px; margin-top: 5px;">
            {{ $t('admin.portMapping.portCountHint') }}
          </div>
          <div
            v-if="portRangePreview"
            style="color: #16a34a; font-size: 12px; margin-top: 5px;"
          >
            <strong>{{ $t('admin.portMapping.portRangePreview') }}:</strong> {{ portRangePreview }}
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.publicPort')"
          prop="hostPort"
        >
          <div style="display: flex; gap: 10px; align-items: start;">
            <el-input-number
              v-model="addForm.hostPort"
              :min="0"
              :max="65535"
              :controls="false"
              :placeholder="$t('admin.portMapping.autoAssignPort')"
              style="flex: 1"
              @change="updatePortRange"
              @blur="checkPortAvailabilityDebounced"
            />
            <el-button
              :loading="checkingPort"
              :disabled="!addForm.hostPort || addForm.hostPort === 0"
              @click="checkPortAvailability"
            >
              {{ $t('admin.portMapping.checkPort') }}
            </el-button>
          </div>
          <div style="color: #909399; font-size: 12px; margin-top: 5px;">
            {{ $t('admin.portMapping.autoAssignPortHint') }}
          </div>
          <!-- 端口检查结果 -->
          <div
            v-if="portCheckResult"
            :style="{ color: portCheckResult.available ? '#67c23a' : '#f56c6c', fontSize: '12px', marginTop: '5px' }"
          >
            <el-icon><CircleCheck v-if="portCheckResult.available" /><CircleClose v-else /></el-icon>
            {{ portCheckResult.message }}
          </div>
          <div
            v-if="portCheckResult && portCheckResult.suggestion"
            style="color: #e6a23c; font-size: 12px; margin-top: 3px;"
          >
            💡 {{ portCheckResult.suggestion }}
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.protocol')"
          prop="protocol"
        >
          <el-radio-group v-model="addForm.protocol">
            <el-radio label="tcp">
              {{ $t('admin.portMapping.protocolTCP') }}
            </el-radio>
            <el-radio label="udp">
              {{ $t('admin.portMapping.protocolUDP') }}
            </el-radio>
            <el-radio label="both">
              {{ $t('admin.portMapping.protocolBoth') }}
            </el-radio>
          </el-radio-group>
        </el-form-item>

        <!-- 映射模式：节点侧 / 控制端转发 -->
        <el-form-item :label="$t('admin.portMapping.mappingMode')">
          <el-radio-group v-model="addForm.mappingType">
            <el-radio label="node">
              {{ $t('admin.portMapping.mappingModeNode') }}
            </el-radio>
            <el-radio label="controller">
              {{ $t('admin.portMapping.mappingModeController') }}
            </el-radio>
          </el-radio-group>
          <div style="margin-top: 4px;">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.portMapping.mappingModeTip') }}
            </el-text>
          </div>
        </el-form-item>

        <!-- 控制端转发目标地址（可选，留空则使用实例私有IP） -->
        <el-form-item
          v-if="addForm.mappingType === 'controller'"
          :label="$t('admin.portMapping.internalHost')"
        >
          <el-input
            v-model="addForm.internalHost"
            :placeholder="$t('admin.portMapping.internalHostPlaceholder')"
          />
          <div style="margin-top: 4px;">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.portMapping.internalHostTip') }}
            </el-text>
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('common.description')"
          prop="description"
        >
          <el-input
            v-model="addForm.description"
            :placeholder="$t('admin.portMapping.descriptionPlaceholder')"
            maxlength="256"
            show-word-limit
          />
        </el-form-item>
      </el-form>
      
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="handleAddDialogClose">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="addLoading"
            @click="submitAdd"
          >
            {{ $t('admin.portMapping.confirmAdd') }}
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>


<script setup>
import { onMounted, onUnmounted } from 'vue'
import { ElMessageBox } from 'element-plus'
import { Plus, Loading, Search, CircleCheck, CircleClose, RefreshRight } from '@element-plus/icons-vue'
import { usePortMappingManagement } from './composables/usePortMappingManagement'

const {
  loading, portMappings, providers, instances, currentPage, pageSize, total,
  selectedPortMappings, searchForm,
  addDialogVisible, addFormRef, addLoading, addForm, addRules,
  checkingPort, portCheckResult,
  supportedInstances, selectedInstanceProvider, portRangePreview, portMappingHint,
  instanceFilterText, filteredInstances, filteredInstancesCount,
  getInstanceProviderType, getProviderTagType,
  loadPortMappings, loadProviders, loadInstances,
  searchPortMappings, resetSearch, isDeletablePort,
  handleSelectionChange, handleSizeChange, handleCurrentChange,
  deletePortMappingHandler, batchDeleteDirect,
  formatTime, openAddDialog, onInstanceChange, submitAdd,
  handleSyncPortMappings, filterInstances,
  updatePortRange, checkPortAvailabilityDebounced, checkPortAvailability,
  cleanupAutoRefresh,
  t
} = usePortMappingManagement()

onMounted(() => {
  loadProviders()
  loadInstances()
  loadPortMappings()
})

onUnmounted(() => {
  cleanupAutoRefresh()
})

// 添加端口对话框关闭（带未保存更改警告）
const handleAddDialogClose = (done) => {
  const isFormDirty = !!(addForm.instanceId || addForm.guestPort || addForm.description)
  if (isFormDirty) {
    ElMessageBox.confirm(
      t('common.unsavedChangesConfirm'),
      t('common.unsavedChanges'),
      {
        confirmButtonText: t('common.discardChanges'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    ).then(() => {
      if (typeof done === 'function') done()
      else addDialogVisible.value = false
    }).catch(() => {})
  } else {
    if (typeof done === 'function') done()
    else addDialogVisible.value = false
  }
}
</script>

<style scoped>
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

<style>
/* 实例选择下拉菜单样式 - 全局样式 */
.instance-select-dropdown {
  max-height: 400px !important;
}

.instance-select-dropdown .el-select-dropdown__list {
  max-height: 380px !important;
}
</style>

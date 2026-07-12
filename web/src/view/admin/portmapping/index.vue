<template>
  <div>
    <el-card class="box-card">
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.portMapping.title') }}</span>
          <div class="header-actions">
            <el-button
              type="primary"
              @click="openAddDialog"
            >
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
                :loading="syncPreviewLoading"
                @click="handleSyncPortMappings"
              >
                {{ $t('admin.portMapping.syncPortMappings') }}
              </el-button>
            </el-tooltip>
            <el-tooltip
              :content="$t('admin.portMapping.repairPortMappingsTooltip')"
              placement="bottom"
            >
              <el-button
                type="danger"
                plain
                :icon="RefreshRight"
                :loading="repairPreviewLoading"
                @click="handleRepairPortMappings"
              >
                {{ $t('admin.portMapping.repairPortMappings') }}
              </el-button>
            </el-tooltip>
          </div>
        </div>
      </template>

      <!-- 端口说明 -->
      <el-alert
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 12px;"
      >
        <template #title>
          <span style="font-size: 12px;">
            {{ $t('admin.portMapping.rangePortInfo') }}
          </span>
        </template>
      </el-alert>

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
          min-width="160"
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
          min-width="150"
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
          min-width="110"
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
          min-width="130"
        />
        <el-table-column
          prop="isIPv6"
          :label="$t('admin.portMapping.labelIPv6')"
          min-width="100"
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

    <el-dialog
      v-model="syncPreviewVisible"
      :title="$t('admin.portMapping.syncPreviewTitle')"
      width="920px"
    >
      <el-alert
        v-if="unhealthySyncProviders.length > 0"
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 12px;"
      >
        <template #title>
          <span>{{ $t('admin.portMapping.syncPreviewProviderErrors', { count: unhealthySyncProviders.length }) }}</span>
        </template>
        <div class="sync-error-list">
          <div
            v-for="provider in unhealthySyncProviders"
            :key="provider.providerId"
          >
            {{ provider.providerName }}: {{ provider.error }}
          </div>
        </div>
      </el-alert>

      <div class="sync-preview-toolbar">
        <el-button
          size="small"
          @click="toggleAllSyncCandidates"
        >
          {{ allSyncSelected ? $t('admin.portMapping.syncUnselectAll') : $t('admin.portMapping.syncSelectAll') }}
        </el-button>
        <el-text type="info">
          {{ $t('admin.portMapping.syncSelectedCount', { selected: selectedSyncPortIds.length, total: syncCandidates.length }) }}
        </el-text>
      </div>

      <el-checkbox-group v-model="selectedSyncPortIds">
        <el-table
          :data="syncCandidates"
          max-height="420"
          border
        >
          <el-table-column
            width="52"
            align="center"
          >
            <template #default="{ row }">
              <el-checkbox :label="row.portId" />
            </template>
          </el-table-column>
          <el-table-column
            prop="providerName"
            :label="$t('admin.portMapping.provider')"
            min-width="160"
          />
          <el-table-column
            prop="instanceName"
            :label="$t('admin.portMapping.instanceName')"
            width="160"
          />
          <el-table-column
            :label="$t('admin.portMapping.publicPort')"
            width="130"
          >
            <template #default="{ row }">
              {{ row.hostPort }}
            </template>
          </el-table-column>
          <el-table-column
            prop="guestPort"
            :label="$t('admin.portMapping.internalPort')"
            min-width="150"
          />
          <el-table-column
            prop="protocol"
            :label="$t('admin.portMapping.protocol')"
            min-width="110"
          />
          <el-table-column
            prop="portType"
            :label="$t('admin.portMapping.portType')"
            width="120"
          />
          <el-table-column
            :label="$t('admin.portMapping.syncReason')"
            min-width="180"
          >
            <template #default="{ row }">
              {{ formatSyncReason(row.reason) }}
            </template>
          </el-table-column>
        </el-table>
      </el-checkbox-group>

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="syncPreviewVisible = false">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="danger"
            :loading="syncSubmitting"
            :disabled="selectedSyncPortIds.length === 0"
            @click="confirmSyncPortMappings"
          >
            {{ $t('admin.portMapping.syncExecuteSelected') }}
          </el-button>
        </span>
      </template>
    </el-dialog>

    <el-dialog
      v-model="repairPreviewVisible"
      :title="$t('admin.portMapping.repairPreviewTitle')"
      width="min(920px, 94vw)"
      append-to-body
    >
      <el-alert
        type="error"
        :closable="false"
        show-icon
        style="margin-bottom: 14px;"
      >
        <template #title>
          {{ $t('admin.portMapping.repairWarningTitle') }}
        </template>
        <div class="repair-warning-text">
          {{ $t('admin.portMapping.repairWarning') }}
        </div>
      </el-alert>

      <el-descriptions
        v-if="!isCompactRepair"
        :column="4"
        border
        class="repair-summary"
      >
        <el-descriptions-item :label="$t('admin.portMapping.repairProviders')">
          {{ repairPreview.providerCount || 0 }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.portMapping.repairRecords')">
          {{ repairPreview.candidateCount || 0 }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.portMapping.repairRules')">
          {{ repairPreview.ruleCount || 0 }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.portMapping.repairRestarts')">
          {{ repairPreview.requiresInstanceRestartCount || 0 }}
        </el-descriptions-item>
      </el-descriptions>

      <div
        v-else
        class="repair-summary-mobile"
      >
        <div class="repair-summary-mobile-item">
          <span>{{ $t('admin.portMapping.repairProviders') }}</span>
          <strong>{{ repairPreview.providerCount || 0 }}</strong>
        </div>
        <div class="repair-summary-mobile-item">
          <span>{{ $t('admin.portMapping.repairRecords') }}</span>
          <strong>{{ repairPreview.candidateCount || 0 }}</strong>
        </div>
        <div class="repair-summary-mobile-item">
          <span>{{ $t('admin.portMapping.repairRules') }}</span>
          <strong>{{ repairPreview.ruleCount || 0 }}</strong>
        </div>
        <div class="repair-summary-mobile-item">
          <span>{{ $t('admin.portMapping.repairRestarts') }}</span>
          <strong>{{ repairPreview.requiresInstanceRestartCount || 0 }}</strong>
        </div>
      </div>

      <div
        v-if="repairCandidates.length > 0"
        class="sync-preview-toolbar"
      >
        <el-button
          size="small"
          @click="toggleAllRepairCandidates"
        >
          {{ allRepairSelected ? $t('admin.portMapping.syncUnselectAll') : $t('admin.portMapping.syncSelectAll') }}
        </el-button>
        <el-text type="info">
          {{ $t('admin.portMapping.repairSelectedCount', { selected: selectedRepairPortIds.length, total: repairCandidates.length }) }}
        </el-text>
      </div>

      <el-checkbox-group v-model="selectedRepairPortIds">
        <el-table
          v-if="!isCompactRepair"
          :data="repairCandidates"
          max-height="360"
          border
        >
          <el-table-column
            width="52"
            align="center"
          >
            <template #default="{ row }">
              <el-checkbox :value="row.portId" />
            </template>
          </el-table-column>
          <el-table-column
            prop="providerName"
            :label="$t('admin.portMapping.provider')"
            min-width="140"
          />
          <el-table-column
            prop="instanceName"
            :label="$t('admin.portMapping.instanceName')"
            min-width="150"
          />
          <el-table-column
            :label="$t('admin.portMapping.publicPort')"
            min-width="130"
          >
            <template #default="{ row }">
              {{ row.hostPortEnd > 0 ? `${row.hostPort}-${row.hostPortEnd}` : row.hostPort }}
            </template>
          </el-table-column>
          <el-table-column
            :label="$t('admin.portMapping.internalPort')"
            min-width="130"
          >
            <template #default="{ row }">
              {{ row.guestPortEnd > 0 ? `${row.guestPort}-${row.guestPortEnd}` : row.guestPort }}
            </template>
          </el-table-column>
          <el-table-column
            prop="protocol"
            :label="$t('admin.portMapping.protocol')"
            width="90"
          />
          <el-table-column
            :label="$t('admin.portMapping.repairImpact')"
            min-width="135"
          >
            <template #default="{ row }">
              <el-tag
                v-if="row.requiresInstanceRestart"
                type="danger"
                size="small"
              >
                {{ $t('admin.portMapping.repairRestartRequired') }}
              </el-tag>
              <el-tag
                v-else
                type="warning"
                size="small"
              >
                {{ $t('admin.portMapping.repairRewriteRule') }}
              </el-tag>
            </template>
          </el-table-column>
        </el-table>

        <div
          v-else
          class="repair-mobile-list"
        >
          <el-checkbox
            v-for="row in repairCandidates"
            :key="row.portId"
            :value="row.portId"
            class="repair-mobile-item"
          >
            <div class="repair-mobile-content">
              <div class="repair-mobile-heading">
                <strong>{{ row.providerName }}</strong>
                <span>{{ row.instanceName }}</span>
              </div>
              <div class="repair-mobile-route">
                <span>
                  <small>{{ $t('admin.portMapping.publicPort') }}</small>
                  <strong>{{ row.hostPortEnd > 0 ? `${row.hostPort}-${row.hostPortEnd}` : row.hostPort }}</strong>
                </span>
                <span>
                  <small>{{ $t('admin.portMapping.internalPort') }}</small>
                  <strong>{{ row.guestPortEnd > 0 ? `${row.guestPort}-${row.guestPortEnd}` : row.guestPort }}</strong>
                </span>
              </div>
              <div class="repair-mobile-meta">
                <span>{{ String(row.protocol || '-').toUpperCase() }}</span>
                <el-tag
                  v-if="row.requiresInstanceRestart"
                  type="danger"
                  size="small"
                >
                  {{ $t('admin.portMapping.repairRestartRequired') }}
                </el-tag>
                <el-tag
                  v-else
                  type="warning"
                  size="small"
                >
                  {{ $t('admin.portMapping.repairRewriteRule') }}
                </el-tag>
              </div>
            </div>
          </el-checkbox>
        </div>
      </el-checkbox-group>

      <el-collapse
        v-if="repairSkipped.length > 0"
        class="repair-skipped"
      >
        <el-collapse-item name="skipped">
          <template #title>
            {{ $t('admin.portMapping.repairSkippedTitle', { count: repairSkipped.length }) }}
          </template>
          <el-table
            v-if="!isCompactRepair"
            :data="repairSkipped"
            max-height="220"
            size="small"
          >
            <el-table-column
              prop="providerName"
              :label="$t('admin.portMapping.provider')"
              min-width="130"
            />
            <el-table-column
              prop="instanceName"
              :label="$t('admin.portMapping.instanceName')"
              min-width="140"
            />
            <el-table-column
              prop="hostPort"
              :label="$t('admin.portMapping.publicPort')"
              width="110"
            />
            <el-table-column
              :label="$t('admin.portMapping.repairSkipReason')"
              min-width="220"
            >
              <template #default="{ row }">
                {{ formatRepairSkipReason(row.reason) }}
              </template>
            </el-table-column>
          </el-table>
          <div
            v-else
            class="repair-skipped-mobile"
          >
            <div
              v-for="row in repairSkipped"
              :key="row.portId"
              class="repair-skipped-mobile-item"
            >
              <div>
                <strong>{{ row.providerName }}</strong>
                <span>{{ row.instanceName || '-' }}</span>
              </div>
              <div>
                <span>{{ $t('admin.portMapping.publicPort') }}: {{ row.hostPort }}</span>
                <span>{{ formatRepairSkipReason(row.reason) }}</span>
              </div>
            </div>
          </div>
        </el-collapse-item>
      </el-collapse>

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="repairPreviewVisible = false">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="danger"
            :loading="repairSubmitting"
            :disabled="selectedRepairPortIds.length === 0"
            @click="confirmRepairPortMappings"
          >
            {{ $t('admin.portMapping.repairExecuteSelected') }}
          </el-button>
        </span>
      </template>
    </el-dialog>

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
              {{ $t('admin.portMapping.noSupportedInstances') }}（{{ $t('admin.portMapping.instancesLoadedButNotSupported', { count: instances.length }) }}）
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
            :disabled="addForm.mappingType === 'controller'"
            :placeholder="$t('admin.portMapping.portCountPlaceholder')"
            style="width: 100%"
            @change="updatePortRange"
          />
          <div style="color: #909399; font-size: 12px; margin-top: 5px;">
            {{ addForm.mappingType === 'controller' ? $t('admin.portMapping.controllerSinglePortHint') : $t('admin.portMapping.portCountHint') }}
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
            {{ portCheckResult.suggestion }}
          </div>
        </el-form-item>
        
        <el-form-item
          :label="$t('admin.portMapping.protocol')"
          prop="protocol"
        >
          <el-radio-group
            v-model="addForm.protocol"
            :disabled="addForm.mappingType === 'controller'"
          >
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
          <div
            v-if="addForm.mappingType === 'controller'"
            style="color: #909399; font-size: 12px; margin-top: 5px;"
          >
            {{ $t('admin.portMapping.controllerTcpHint') }}
          </div>
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
import { onMounted, onUnmounted, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { Loading, Search, CircleCheck, CircleClose, RefreshRight } from '@element-plus/icons-vue'
import { usePortMappingManagement } from './composables/usePortMappingManagement'

const {
  loading, portMappings, providers, instances, currentPage, pageSize, total,
  selectedPortMappings, searchForm,
  syncPreviewVisible, syncPreviewLoading, syncSubmitting,
  selectedSyncPortIds, syncCandidates, unhealthySyncProviders, allSyncSelected,
  repairPreviewVisible, repairPreviewLoading, repairSubmitting, repairPreview,
  selectedRepairPortIds, repairCandidates, repairSkipped, allRepairSelected,
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
  handleSyncPortMappings, confirmSyncPortMappings, toggleAllSyncCandidates, formatSyncReason, filterInstances,
  handleRepairPortMappings, confirmRepairPortMappings, toggleAllRepairCandidates, formatRepairSkipReason,
  updatePortRange, checkPortAvailabilityDebounced, checkPortAvailability,
  cleanupAutoRefresh,
  t
} = usePortMappingManagement()

const isCompactRepair = ref(false)
let compactRepairMediaQuery

const updateCompactRepair = (event) => {
  isCompactRepair.value = event.matches
}

onMounted(() => {
  compactRepairMediaQuery = window.matchMedia('(max-width: 600px)')
  isCompactRepair.value = compactRepairMediaQuery.matches
  compactRepairMediaQuery.addEventListener('change', updateCompactRepair)
  loadProviders()
  loadInstances()
  loadPortMappings()
})

onUnmounted(() => {
  compactRepairMediaQuery?.removeEventListener('change', updateCompactRepair)
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
  gap: 12px;
  
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
  flex-wrap: wrap;
  justify-content: flex-end;
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

.sync-preview-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.sync-error-list {
  margin-top: 6px;
  line-height: 1.6;
}

.repair-warning-text {
  line-height: 1.6;
}

.repair-summary {
  margin-bottom: 14px;
}

.repair-summary-mobile {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  margin-bottom: 14px;
  border-top: 1px solid var(--border-color);
  border-left: 1px solid var(--border-color);
}

.repair-summary-mobile-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
  padding: 10px 12px;
  border-right: 1px solid var(--border-color);
  border-bottom: 1px solid var(--border-color);
}

.repair-summary-mobile-item span {
  min-width: 0;
  color: var(--text-color-secondary);
}

.repair-summary-mobile-item strong {
  flex: 0 0 auto;
  color: var(--text-color-primary);
}

.repair-mobile-list {
  display: grid;
  gap: 10px;
}

.repair-mobile-item {
  display: flex;
  align-items: flex-start;
  width: 100%;
  height: auto;
  margin: 0;
  padding: 12px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
}

.repair-mobile-item :deep(.el-checkbox__input) {
  margin-top: 3px;
}

.repair-mobile-item :deep(.el-checkbox__label) {
  flex: 1;
  min-width: 0;
  padding-left: 10px;
  white-space: normal;
}

.repair-mobile-content {
  display: grid;
  gap: 10px;
  min-width: 0;
}

.repair-mobile-heading,
.repair-mobile-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.repair-mobile-heading strong,
.repair-mobile-heading span {
  min-width: 0;
  overflow-wrap: anywhere;
}

.repair-mobile-heading span {
  color: var(--text-color-secondary);
  text-align: right;
}

.repair-mobile-route {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.repair-mobile-route > span {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.repair-mobile-route small {
  color: var(--text-color-secondary);
}

.repair-mobile-route strong {
  overflow-wrap: anywhere;
}

.repair-mobile-meta > span:first-child {
  color: var(--text-color-secondary);
  font-size: 12px;
}

.repair-skipped-mobile {
  display: grid;
  gap: 8px;
  padding-bottom: 8px;
}

.repair-skipped-mobile-item {
  display: grid;
  gap: 6px;
  padding: 10px 0;
  border-bottom: 1px solid var(--border-color);
}

.repair-skipped-mobile-item > div {
  display: flex;
  justify-content: space-between;
  gap: 10px;
}

.repair-skipped-mobile-item span {
  min-width: 0;
  color: var(--text-color-secondary);
  overflow-wrap: anywhere;
}

.repair-skipped {
  margin-top: 14px;
}

@media (max-width: 900px) {
  .card-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .header-actions {
    justify-content: flex-start;
    width: 100%;
  }
}

@media (max-width: 600px) {
  .repair-warning-text {
    font-size: 13px;
  }

  .sync-preview-toolbar {
    align-items: flex-start;
    flex-direction: column;
    gap: 8px;
  }

  .dialog-footer {
    width: 100%;
  }

  .dialog-footer .el-button {
    flex: 1;
    min-width: 0;
  }
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

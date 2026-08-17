<template>
  <!-- IPv6 地址池/节点地址文件同步。范围按需分配，不会在前端展开。 -->
  <template v-if="modelValue.networkType === 'nat_ipv4_ipv6' || modelValue.networkType === 'dedicated_ipv4_ipv6' || modelValue.networkType === 'ipv6_only'">
    <el-divider
      content-position="left"
      style="margin-top: 24px;"
    >
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.ipv6Pool.management') }}</span>
    </el-divider>
    <el-alert
      v-if="!modelValue.id"
      type="info"
      :closable="false"
      :title="$t('admin.providers.ipv6Pool.newProviderNote')"
      style="margin-bottom: 16px;"
    />
    <template v-else>
      <el-alert
        v-if="!supportsStaticIPv6"
        type="warning"
        :title="$t('admin.providers.ipv6Pool.staticAllocationUnsupported')"
        :description="$t('admin.providers.ipv6Pool.staticAllocationUnsupportedTip', { type: modelValue.type || '-' })"
        :closable="false"
        show-icon
        style="margin-bottom: 16px;"
      />
      <el-alert
        v-else-if="requiresRoutedStaticIPv6"
        type="info"
        :title="$t('admin.providers.ipv6Pool.routedAllocationRequired')"
        :description="$t('admin.providers.ipv6Pool.routedAllocationRequiredTip', { type: modelValue.type || '-' })"
        :closable="false"
        show-icon
        style="margin-bottom: 16px;"
      />
      <el-form-item :label="$t('admin.providers.ipv6Pool.filePath')">
        <div class="ipv6-file-config">
          <el-input
            v-model="modelValue.ipv6AddressFilePath"
            :placeholder="$t('admin.providers.ipv6Pool.filePathPlaceholder')"
            :disabled="!canManageStaticIPv6Pool"
            clearable
          />
          <div class="ipv6-file-actions">
            <el-button
              :icon="DocumentChecked"
              :loading="ipv6FileSaving"
              :disabled="!canManageStaticIPv6Pool || ipv6FileSyncing"
              @click="saveIPv6FilePath"
            >
              {{ $t('admin.providers.ipv6Pool.fileSaveBtn') }}
            </el-button>
            <el-popconfirm
              :title="$t('admin.providers.ipv6Pool.fileClearConfirm')"
              @confirm="clearIPv6FilePath"
            >
              <template #reference>
                <el-button
                  type="danger"
                  plain
                  :icon="Delete"
                  :loading="ipv6FileSaving"
                  :disabled="!hasIPv6FilePath || ipv6FileSyncing"
                >
                  {{ $t('admin.providers.ipv6Pool.fileClearBtn') }}
                </el-button>
              </template>
            </el-popconfirm>
            <el-button
              type="primary"
              :icon="Refresh"
              :loading="ipv6FileSyncing"
              :disabled="!canManageStaticIPv6Pool || !hasIPv6FilePath || ipv6FileSaving"
              @click="syncIPv6File"
            >
              {{ $t('admin.providers.ipv6Pool.syncBtn') }}
            </el-button>
          </div>
          <div class="ipv6-file-state">
            <el-tag
              size="small"
              :type="hasIPv6FilePath ? 'success' : 'info'"
            >
              {{ hasIPv6FilePath ? $t('admin.providers.ipv6Pool.fileConfigured') : $t('admin.providers.ipv6Pool.autoDetection') }}
            </el-tag>
            <el-tag
              size="small"
              :type="supportsStaticIPv6 ? 'success' : 'warning'"
            >
              {{ supportsStaticIPv6 ? $t('admin.providers.ipv6Pool.staticAllocationSupported') : $t('admin.providers.ipv6Pool.staticAllocationUnavailable') }}
            </el-tag>
            <el-text
              size="small"
              type="info"
            >
              {{ hasIPv6FilePath ? $t('admin.providers.ipv6Pool.filePathTip') : $t('admin.providers.ipv6Pool.autoDetectionTip') }}
            </el-text>
          </div>
        </div>
      </el-form-item>

      <el-alert
        v-if="modelValue.ipv6AddressFileSyncError"
        type="error"
        :title="$t('admin.providers.ipv6Pool.lastSyncError')"
        :description="modelValue.ipv6AddressFileSyncError"
        :closable="false"
        show-icon
        style="margin-bottom: 16px;"
      />

      <el-descriptions
        v-if="ipv6SyncResult || ipv6LastSyncedAt"
        :title="$t('admin.providers.ipv6Pool.syncStatus')"
        :column="3"
        border
        size="small"
        class="ipv6-sync-result"
      >
        <el-descriptions-item :label="$t('admin.providers.ipv6Pool.lastSyncedAt')">
          {{ ipv6LastSyncedAt || '-' }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.providers.ipv6Pool.parsedCount')">
          {{ ipv6SyncResult?.parsedCount ?? '-' }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.providers.ipv6Pool.remoteReadCount')">
          {{ ipv6SyncResult?.remoteReadCount ?? '-' }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.providers.ipv6Pool.addedCount')">
          {{ syncItemCount(ipv6SyncResult?.added) }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.providers.ipv6Pool.removedCount')">
          {{ syncItemCount(ipv6SyncResult?.removed) }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.providers.ipv6Pool.preservedCount')">
          {{ syncItemCount(ipv6SyncResult?.preservedAllocated) }}
        </el-descriptions-item>
        <el-descriptions-item :label="$t('admin.providers.ipv6Pool.invalidLines')">
          <el-tooltip
            v-if="ipv6SyncResult?.invalidLines?.length"
            :content="ipv6SyncResult.invalidLines.join(', ')"
            placement="top"
          >
            <el-tag
              type="warning"
              size="small"
            >
              {{ ipv6SyncResult.invalidLines.length }}
            </el-tag>
          </el-tooltip>
          <span v-else>{{ ipv6SyncResult ? 0 : '-' }}</span>
        </el-descriptions-item>
      </el-descriptions>

      <el-row
        :gutter="16"
        style="margin-bottom: 16px;"
      >
        <el-col :span="8">
          <el-statistic
            :title="$t('admin.providers.ipv6Pool.total')"
            :value="ipv6PoolStats.total"
          />
        </el-col>
        <el-col :span="8">
          <el-statistic
            :title="$t('admin.providers.ipv6Pool.allocated')"
            :value="ipv6PoolStats.allocated"
          />
        </el-col>
        <el-col :span="8">
          <el-statistic
            :title="$t('admin.providers.ipv6Pool.available')"
            :value="ipv6PoolStats.available"
          />
        </el-col>
      </el-row>
      <el-form-item :label="$t('admin.providers.ipv6Pool.addresses')">
        <div class="ipv6-pool-editor">
          <el-input
            v-model="newIPv6Addresses"
            type="textarea"
            :rows="4"
            :placeholder="$t('admin.providers.ipv6Pool.addressesPlaceholder')"
            :disabled="!canManageStaticIPv6Pool"
          />
          <el-space wrap>
            <el-button
              type="primary"
              :loading="ipv6PoolSaving"
              :disabled="!canManageStaticIPv6Pool"
              @click="addIPv6ToPool"
            >
              {{ $t('admin.providers.ipv6Pool.addBtn') }}
            </el-button>
            <el-popconfirm
              :title="$t('admin.providers.ipv6Pool.clearConfirm')"
              @confirm="clearIPv6Pool"
            >
              <template #reference>
                <el-button
                  type="danger"
                  plain
                >
                  {{ $t('admin.providers.ipv6Pool.clearBtn') }}
                </el-button>
              </template>
            </el-popconfirm>
          </el-space>
        </div>
      </el-form-item>
      <el-form-item :label="$t('admin.providers.ipv6Pool.list')">
        <el-table
          v-loading="ipv6PoolLoading"
          :data="ipv6PoolEntries"
          style="width: 100%"
          size="small"
          max-height="240"
        >
          <el-table-column
            :label="$t('admin.providers.ipv6Pool.address')"
            prop="address"
            min-width="220"
            show-overflow-tooltip
          />
          <el-table-column
            :label="$t('admin.providers.ipv6Pool.status')"
            min-width="100"
          >
            <template #default="{ row }">
              <el-tag
                :type="row.is_range ? 'info' : (row.is_allocated ? 'warning' : 'success')"
                size="small"
              >
                {{ row.is_range ? $t('admin.providers.ipv6Pool.statusRange') : (row.is_allocated ? $t('admin.providers.ipv6Pool.statusAllocated') : $t('admin.providers.ipv6Pool.statusFree')) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column
            :label="$t('admin.providers.ipv6Pool.instance')"
            prop="instance_id"
            min-width="100"
          >
            <template #default="{ row }">
              <span>{{ row.instance_id || '-' }}</span>
            </template>
          </el-table-column>
          <el-table-column
            width="80"
            align="center"
          >
            <template #default="{ row }">
              <el-popconfirm
                v-if="!row.is_allocated && !row.is_range"
                :title="$t('admin.providers.ipv6Pool.deleteConfirm')"
                @confirm="deleteIPv6Entry(row.id)"
              >
                <template #reference>
                  <el-button
                    type="danger"
                    link
                    size="small"
                  >
                    {{ $t('common.delete') }}
                  </el-button>
                </template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>
      </el-form-item>
    </template>
  </template>
</template>

<script setup>
import { computed } from 'vue'
import { Delete, DocumentChecked, Refresh } from '@element-plus/icons-vue'
import { useIPv6Pool } from './composables/useIPv6Pool'
import { requiresRoutedStaticIPv6Provider, supportsStaticIPv6Provider } from '@/utils/ipv6Capabilities'

const props = defineProps({
  modelValue: {
    type: Object,
    required: true
  }
})
const emit = defineEmits(['provider-updated'])

const hasIPv6FilePath = computed(() => Boolean(String(props.modelValue.ipv6AddressFilePath || '').trim()))
const supportsStaticIPv6 = computed(() => supportsStaticIPv6Provider(props.modelValue.type))
const requiresRoutedStaticIPv6 = computed(() => requiresRoutedStaticIPv6Provider(props.modelValue.type))
const canManageStaticIPv6Pool = computed(() => supportsStaticIPv6.value && !requiresRoutedStaticIPv6.value)
const syncItemCount = value => Array.isArray(value) ? value.length : '-'

const {
  ipv6PoolEntries,
  ipv6PoolStats,
  ipv6PoolLoading,
  newIPv6Addresses,
  ipv6PoolSaving,
  ipv6FileSaving,
  ipv6FileSyncing,
  ipv6SyncResult,
  ipv6LastSyncedAt,
  addIPv6ToPool,
  clearIPv6Pool,
  deleteIPv6Entry,
  saveIPv6FilePath,
  clearIPv6FilePath,
  syncIPv6File,
} = useIPv6Pool(props, updates => emit('provider-updated', updates))
</script>

<style scoped>
.ipv6-file-config,
.ipv6-pool-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.ipv6-file-actions,
.ipv6-file-state {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.ipv6-sync-result {
  margin-bottom: 16px;
}
</style>

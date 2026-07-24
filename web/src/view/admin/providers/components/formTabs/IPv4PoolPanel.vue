<template>
  <!-- IPv4 地址池管理（仅对 dedicated_ipv4 / dedicated_ipv4_ipv6 显示） -->
  <template v-if="modelValue.networkType === 'dedicated_ipv4' || modelValue.networkType === 'dedicated_ipv4_ipv6'">
    <el-divider
      content-position="left"
      style="margin-top: 24px;"
    >
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.ipv4Pool.management') }}</span>
    </el-divider>

    <!-- 新提供商提示 -->
    <el-alert
      v-if="!modelValue.id"
      type="info"
      :closable="false"
      :title="$t('admin.providers.ipv4Pool.newProviderNote')"
      style="margin-bottom: 16px;"
    />

    <template v-else>
      <!-- 池统计 -->
      <el-row
        :gutter="16"
        style="margin-bottom: 16px;"
      >
        <el-col :span="8">
          <el-statistic
            :title="$t('admin.providers.ipv4Pool.total')"
            :value="poolStats.total"
          />
        </el-col>
        <el-col :span="8">
          <el-statistic
            :title="$t('admin.providers.ipv4Pool.allocated')"
            :value="poolStats.allocated"
          />
        </el-col>
        <el-col :span="8">
          <el-statistic
            :title="$t('admin.providers.ipv4Pool.available')"
            :value="poolStats.available"
          />
        </el-col>
      </el-row>

      <!-- 添加地址 -->
      <el-form-item :label="$t('admin.providers.ipv4Pool.addresses')">
        <div style="width: 100%;">
          <el-input
            v-model="newAddresses"
            type="textarea"
            :rows="4"
            :placeholder="$t('admin.providers.ipv4Pool.addressesPlaceholder')"
            style="width: 100%; margin-bottom: 8px;"
          />
          <el-space>
            <el-button
              type="primary"
              :loading="saving"
              @click="addToPool"
            >
              {{ $t('admin.providers.ipv4Pool.addBtn') }}
            </el-button>
            <el-popconfirm
              :title="$t('admin.providers.ipv4Pool.clearConfirm')"
              @confirm="clearPool"
            >
              <template #reference>
                <el-button
                  type="danger"
                  plain
                >
                  {{ $t('admin.providers.ipv4Pool.clearBtn') }}
                </el-button>
              </template>
            </el-popconfirm>
          </el-space>
        </div>
      </el-form-item>
      <!-- 当前地址列表 -->
      <el-form-item :label="$t('admin.providers.ipv4Pool.list')">
        <el-table
          v-loading="poolLoading"
          :data="poolEntries"
          style="width: 100%"
          size="small"
          max-height="240"
        >
          <el-table-column
            :label="$t('admin.providers.ipv4Pool.address')"
            prop="address"
            min-width="140"
          />
          <el-table-column
            :label="$t('admin.providers.ipv4Pool.status')"
            min-width="100"
          >
            <template #default="{ row }">
              <el-tag
                :type="row.is_allocated ? 'warning' : 'success'"
                size="small"
              >
                {{ row.is_allocated ? $t('admin.providers.ipv4Pool.statusAllocated') : $t('admin.providers.ipv4Pool.statusFree') }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column
            :label="$t('admin.providers.ipv4Pool.instance')"
            prop="instance_id"
            min-width="110"
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
                v-if="!row.is_allocated"
                :title="$t('admin.providers.ipv4Pool.deleteConfirm')"
                @confirm="deleteEntry(row.id)"
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
import { useIPv4Pool } from './composables/useIPv4Pool'

const props = defineProps({
  modelValue: {
    type: Object,
    required: true
  }
})

const {
  poolEntries,
  poolStats,
  poolLoading,
  newAddresses,
  saving,
  addToPool,
  clearPool,
  deleteEntry,
} = useIPv4Pool(props)
</script>

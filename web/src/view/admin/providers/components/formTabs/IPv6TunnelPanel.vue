<template>
  <section class="ipv6-tunnel-panel">
    <div class="tunnel-toolbar">
      <div>
        <strong>{{ $t('admin.providers.ipv6Pool.tunnels') }}</strong>
        <el-text
          size="small"
          type="info"
          class="tunnel-help"
        >
          {{ $t('admin.providers.ipv6Pool.tunnelHelp') }}
        </el-text>
      </div>
      <el-space wrap>
        <el-button
          :icon="Refresh"
          :loading="loading"
          :disabled="!providerId"
          @click="check"
        >
          {{ $t('admin.providers.ipv6Pool.tunnelCheck') }}
        </el-button>
        <el-button
          type="primary"
          :icon="Plus"
          :disabled="!providerId"
          @click="openCreate"
        >
          {{ $t('admin.providers.ipv6Pool.tunnelAdd') }}
        </el-button>
      </el-space>
    </div>

    <el-alert
      :title="$t('admin.providers.ipv6Pool.tunnelWarning')"
      type="info"
      :closable="false"
      show-icon
      class="tunnel-warning"
    />

    <el-table
      v-loading="loading"
      :data="tunnels"
      size="small"
      max-height="360"
      row-key="id"
    >
      <el-table-column
        :label="$t('admin.providers.ipv6Pool.tunnelName')"
        min-width="150"
      >
        <template #default="{ row }">
          <div>{{ row.name }}</div>
          <el-text
            size="small"
            type="info"
          >
            {{ row.interfaceName }} · {{ row.mode }}
          </el-text>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.ipv6Pool.tunnelEndpoint')"
        min-width="205"
      >
        <template #default="{ row }">
          <div>{{ row.localIpv4 }} → {{ row.remoteIpv4 }}</div>
          <el-text
            size="small"
            type="info"
          >
            {{ row.localIpv6 }} via {{ row.remoteIpv6 }}
          </el-text>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.ipv6Pool.tunnelStatus')"
        width="115"
      >
        <template #default="{ row }">
          <el-tag
            size="small"
            :type="statusType(row.status)"
          >
            {{ statusText(row.status) }}
          </el-tag>
          <el-tooltip
            v-if="row.lastError"
            :content="row.lastError"
            placement="top"
          >
            <el-icon class="tunnel-error">
              <WarningFilled />
            </el-icon>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.ipv6Pool.tunnelRoute')"
        min-width="135"
      >
        <template #default="{ row }">
          <div>{{ row.defaultRoute ? $t('admin.providers.ipv6Pool.tunnelDefaultRoute') : $t('admin.providers.ipv6Pool.tunnelNoDefaultRoute') }}</div>
          <el-text
            size="small"
            type="info"
          >
            {{ row.routedCidr || '-' }}
          </el-text>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('common.actions')"
        width="170"
        align="right"
      >
        <template #default="{ row }">
          <el-tooltip :content="row.enabled ? $t('admin.providers.ipv6Pool.tunnelDisable') : $t('admin.providers.ipv6Pool.tunnelEnable')">
            <el-button
              link
              :type="row.enabled ? 'warning' : 'success'"
              :icon="row.enabled ? VideoPause : VideoPlay"
              :loading="isBusy(row.id)"
              @click="toggle(row)"
            />
          </el-tooltip>
          <el-tooltip :content="$t('common.edit')">
            <el-button
              link
              :icon="EditPen"
              :disabled="isBusy(row.id)"
              @click="openEdit(row)"
            />
          </el-tooltip>
          <el-tooltip :content="$t('common.delete')">
            <el-button
              link
              type="danger"
              :icon="Delete"
              :loading="isBusy(row.id)"
              @click="remove(row)"
            />
          </el-tooltip>
        </template>
      </el-table-column>
    </el-table>

    <el-empty
      v-if="!loading && tunnels.length === 0"
      :description="$t('admin.providers.ipv6Pool.tunnelEmpty')"
      :image-size="60"
    />

    <el-dialog
      v-model="dialogVisible"
      :title="editingTunnel ? $t('admin.providers.ipv6Pool.tunnelEdit') : $t('admin.providers.ipv6Pool.tunnelAdd')"
      width="min(760px, calc(100vw - 32px))"
      destroy-on-close
      append-to-body
    >
      <el-form
        ref="tunnelFormRef"
        :model="form"
        :rules="rules"
        label-width="125px"
        class="tunnel-form"
      >
        <el-row :gutter="16">
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item
              :label="$t('admin.providers.ipv6Pool.tunnelName')"
              prop="name"
            >
              <el-input
                v-model="form.name"
                maxlength="64"
                show-word-limit
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item
              :label="$t('admin.providers.ipv6Pool.tunnelMode')"
              prop="mode"
            >
              <el-select
                v-model="form.mode"
                style="width: 100%"
              >
                <el-option
                  label="SIT / 6in4"
                  value="sit"
                />
                <el-option
                  label="GRE"
                  value="gre"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item
              :label="$t('admin.providers.ipv6Pool.tunnelInterface')"
              prop="interfaceName"
            >
              <el-input
                v-model="form.interfaceName"
                maxlength="15"
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item :label="$t('admin.providers.ipv6Pool.tunnelRoutedCidr')">
              <el-input
                v-model="form.routedCidr"
                placeholder="2001:db8:1234::/64"
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item
              :label="$t('admin.providers.ipv6Pool.tunnelLocalIpv4')"
            >
              <el-input
                v-model="form.localIpv4"
                :placeholder="$t('admin.providers.ipv6Pool.tunnelLocalIpv4Placeholder')"
              >
                <template #append>
                  <el-button
                    :loading="detectingLocalIpv4"
                    :disabled="!providerId"
                    @click="detectLocalIPv4({ interactive: true })"
                  >
                    {{ $t('admin.providers.ipv6Pool.tunnelDetectLocalIpv4') }}
                  </el-button>
                </template>
              </el-input>
              <el-text
                size="small"
                type="info"
                class="local-ipv4-tip"
              >
                {{ $t('admin.providers.ipv6Pool.tunnelLocalIpv4Tip') }}
              </el-text>
            </el-form-item>
          </el-col>
          <el-col
            v-if="detectionError"
            :span="24"
          >
            <el-alert
              :title="$t('admin.providers.ipv6Pool.tunnelDetectFailed')"
              :description="detectionError"
              type="warning"
              :closable="false"
              show-icon
              class="tunnel-detect-error"
            />
          </el-col>
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item
              :label="$t('admin.providers.ipv6Pool.tunnelRemoteIpv4')"
              prop="remoteIpv4"
            >
              <el-input
                v-model="form.remoteIpv4"
                placeholder="198.51.100.1"
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item
              :label="$t('admin.providers.ipv6Pool.tunnelLocalIpv6')"
              prop="localIpv6"
            >
              <el-input
                v-model="form.localIpv6"
                placeholder="2001:db8::2/64"
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="12"
          >
            <el-form-item
              :label="$t('admin.providers.ipv6Pool.tunnelRemoteIpv6')"
              prop="remoteIpv6"
            >
              <el-input
                v-model="form.remoteIpv6"
                placeholder="2001:db8::1"
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="8"
          >
            <el-form-item :label="$t('admin.providers.ipv6Pool.tunnelMtu')">
              <el-input-number
                v-model="form.mtu"
                :min="1280"
                :max="9000"
                :controls="false"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="8"
          >
            <el-form-item :label="$t('admin.providers.ipv6Pool.tunnelTtl')">
              <el-input-number
                v-model="form.ttl"
                :min="1"
                :max="255"
                :controls="false"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col
            :xs="24"
            :sm="8"
          >
            <el-form-item :label="$t('admin.providers.ipv6Pool.tunnelMetric')">
              <el-input-number
                v-model="form.routeMetric"
                :min="1"
                :max="32766"
                :controls="false"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item :label="$t('admin.providers.ipv6Pool.tunnelDefaultRoute')">
          <el-switch v-model="form.defaultRoute" />
          <el-text
            size="small"
            type="warning"
            class="route-tip"
          >
            {{ $t('admin.providers.ipv6Pool.tunnelDefaultRouteTip') }}
          </el-text>
        </el-form-item>
        <el-form-item
          v-if="!editingTunnel"
          :label="$t('admin.providers.ipv6Pool.tunnelEnableAfterCreate')"
        >
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="saving"
          @click="submit"
        >
          {{ $t('common.save') }}
        </el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup>
import { computed, ref } from 'vue'
import { Delete, EditPen, Plus, Refresh, VideoPause, VideoPlay, WarningFilled } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { useIPv6Tunnels } from './composables/useIPv6Tunnels'

const props = defineProps({
  providerId: {
    type: [Number, String],
    default: 0
  }
})

const providerId = computed(() => Number(props.providerId) || 0)
const { t } = useI18n()
const tunnelFormRef = ref(null)
const requiredRule = { required: true, message: t('admin.providers.ipv6Pool.tunnelRequired'), trigger: 'blur' }
const rules = {
  name: [requiredRule],
  mode: [requiredRule],
  interfaceName: [requiredRule],
  remoteIpv4: [requiredRule],
  localIpv6: [requiredRule],
  remoteIpv6: [requiredRule]
}
const {
  tunnels,
  loading,
  saving,
  dialogVisible,
  editingTunnel,
  form,
  isBusy,
  detectingLocalIpv4,
  detectionError,
  openCreate,
  openEdit,
  detectLocalIPv4,
  submit: persistTunnel,
  toggle,
  check,
  remove
} = useIPv6Tunnels(providerId)

const submit = async () => {
  try {
    await tunnelFormRef.value?.validate()
  } catch {
    return
  }
  await persistTunnel()
}

const statusType = status => ({ active: 'success', inactive: 'info', pending: 'warning', error: 'danger' }[status] || 'info')
const statusText = status => ({
  active: t('admin.providers.ipv6Pool.tunnelStatusActive'),
  inactive: t('admin.providers.ipv6Pool.tunnelStatusInactive'),
  pending: t('admin.providers.ipv6Pool.tunnelStatusPending'),
  error: t('admin.providers.ipv6Pool.tunnelStatusError')
}[status] || status || '-')
</script>

<style scoped>
.ipv6-tunnel-panel {
  width: 100%;
  margin-top: 18px;
}

.tunnel-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.tunnel-help {
  display: block;
  margin-top: 4px;
}

.tunnel-warning {
  margin-bottom: 12px;
}

.tunnel-error {
  margin-left: 4px;
  color: var(--el-color-danger);
  vertical-align: middle;
}

.tunnel-form {
  max-height: 58vh;
  overflow-y: auto;
  padding-right: 8px;
}

.route-tip {
  margin-left: 10px;
}

.local-ipv4-tip {
  display: block;
  margin-top: 6px;
  line-height: 1.35;
}

.tunnel-detect-error {
  margin-bottom: 14px;
}

:global(.ipv6-tunnel-error-dialog .el-message-box__message) {
  max-height: 45vh;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

@media (max-width: 768px) {
  .tunnel-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .tunnel-toolbar .el-space {
    width: 100%;
  }

  .tunnel-toolbar .el-button {
    flex: 1;
  }

  .tunnel-form :deep(.el-form-item) {
    display: block;
  }

  .tunnel-form :deep(.el-form-item__label) {
    width: auto !important;
    justify-content: flex-start;
  }

  .tunnel-form :deep(.el-form-item__content) {
    margin-left: 0 !important;
  }
}
</style>

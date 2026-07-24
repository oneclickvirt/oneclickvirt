<template>
  <el-dialog
    :model-value="modelValue"
    :title="$t('admin.instances.egressTitle')"
    width="min(840px, 94vw)"
    destroy-on-close
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div
      v-loading="loading"
      class="egress-dialog"
    >
      <el-alert
        v-if="status?.agent_error"
        type="error"
        :title="$t('admin.instances.egressAgentUnavailable')"
        :description="status.agent_error"
        show-icon
        :closable="false"
      />
      <el-alert
        v-else-if="status && !status.native_supported"
        type="warning"
        :title="$t('admin.instances.egressNativeUnsupported')"
        :description="(status.unsupported_reasons || []).join('; ')"
        show-icon
        :closable="false"
      />
      <el-alert
        v-if="status?.binding && !status?.effective_verified"
        type="warning"
        :title="$t('admin.instances.egressNotVerified')"
        show-icon
        :closable="false"
      />
      <el-alert
        v-if="form.mode !== 'native'"
        type="info"
        :title="$t('admin.instances.egressExternalAdapterRequired')"
        :description="$t('admin.instances.egressExternalAdapterDescription', { mode: form.mode })"
        show-icon
        :closable="false"
      />

      <section class="egress-section">
        <div class="section-heading">
          {{ $t('admin.instances.egressRuntimeStatus') }}
          <el-button
            text
            :loading="loading"
            @click="loadStatus"
          >
            <el-icon><Refresh /></el-icon>
            {{ $t('common.refresh') }}
          </el-button>
        </div>
        <el-descriptions
          :column="2"
          border
          size="small"
        >
          <el-descriptions-item :label="$t('admin.instances.egressAgent')">
            <el-tag :type="status?.agent_installed && status?.agent_connected ? 'success' : 'danger'">
              {{ status?.agent_installed && status?.agent_connected ? $t('common.normal') : $t('admin.instances.egressUnavailable') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressCapability')">
            <el-tag :type="status?.capabilities?.supported ? 'success' : 'warning'">
              {{ status?.capabilities?.supported ? $t('admin.instances.egressSupported') : $t('admin.instances.egressUnsupported') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressBindingState')">
            <el-tag :type="stateTagType(status?.binding?.state)">
              {{ status?.binding?.state || $t('admin.instances.egressUnbound') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressFailClosedRequired')">
            <el-tag :type="status?.fail_closed_required ? 'success' : 'info'">
              {{ status?.fail_closed_required ? $t('common.enabled') : $t('common.disabled') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressFailClosedEnforced')">
            <el-tag
              :type="status?.fail_closed_enforced === true ? 'success' : status?.fail_closed_enforced === false ? 'danger' : 'warning'"
            >
              {{ status?.fail_closed_enforced === true
                ? $t('admin.instances.egressFailClosedEnforcedYes')
                : status?.fail_closed_enforced === false
                  ? $t('admin.instances.egressFailClosedEnforcedNo')
                  : $t('admin.instances.egressFailClosedEnforcedUnknown') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressExpectedIp')">
            {{ displayedEgress || $t('admin.instances.notSet') }}
            <el-tag
              v-if="displayedEgress"
              size="small"
              :type="status?.effective_verified ? 'success' : 'warning'"
            >
              {{ status?.effective_verified ? $t('admin.instances.egressVerified') : $t('admin.instances.egressUnverified') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressTunnel')">
            <template v-if="selectedProfile">
              <el-tag :type="selectedProfile.tunnel_ready ? 'success' : 'warning'">
                {{ selectedProfile.tunnel_ready ? $t('admin.instances.egressTunnelReady') : $t('admin.instances.egressTunnelPending') }}
              </el-tag>
              <span
                v-if="selectedProfile.last_handshake_at"
                class="inline-detail"
              >
                {{ formatUnixTime(selectedProfile.last_handshake_at) }}
              </span>
            </template>
            <span v-else>{{ $t('admin.instances.notSet') }}</span>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressHostInterfaceV4')">
            {{ bindingInterfaceV4 || $t('admin.instances.notSet') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressHostInterfaceV6')">
            {{ bindingInterfaceV6 || $t('admin.instances.notSet') }}
          </el-descriptions-item>
        </el-descriptions>
      </section>

      <section class="egress-section">
        <div class="section-heading">
          {{ $t('admin.instances.egressTraffic') }}
        </div>
        <el-descriptions
          :column="3"
          border
          size="small"
        >
          <el-descriptions-item :label="$t('admin.instances.inboundTraffic')">
            {{ formatBytes(status?.traffic?.bytes_in) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.outboundTraffic')">
            {{ formatBytes(status?.traffic?.bytes_out) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressDropped')">
            {{ formatBytes(status?.traffic?.dropped_bytes) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.pmacctInterface')">
            {{ (status?.traffic?.interfaces || []).join(', ') || $t('admin.instances.notSet') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.egressCounterSource')">
            {{ status?.traffic?.source || $t('admin.instances.notSet') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('common.updatedAt')">
            {{ trafficUpdatedAt }}
          </el-descriptions-item>
        </el-descriptions>
      </section>

      <section class="egress-section">
        <div class="section-heading">
          {{ $t('admin.instances.egressConfiguration') }}
        </div>
        <el-form
          :model="form"
          label-width="145px"
          label-position="right"
        >
          <el-form-item
            v-if="profiles.length"
            :label="$t('admin.instances.egressExistingProfile')"
          >
            <el-select
              v-model="selectedProfileId"
              clearable
              style="width: 100%"
              @change="selectProfile"
            >
              <el-option
                v-for="profile in profiles"
                :key="profile.id"
                :label="`${profile.id} (${profile.status})`"
                :value="profile.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('admin.instances.egressProfileId')">
            <el-input
              v-model="form.profileId"
              maxlength="128"
            />
          </el-form-item>
          <el-form-item :label="$t('admin.instances.egressMode')">
            <el-segmented
              v-model="form.mode"
              :options="modeOptions"
              block
            />
          </el-form-item>
          <el-form-item :label="$t('admin.instances.egressTunnelType')">
            <el-select
              v-model="form.tunnelType"
              style="width: 100%"
            >
              <el-option
                label="WireGuard"
                value="wireguard"
              />
              <el-option
                label="IPsec"
                value="ipsec"
              />
              <el-option
                :label="$t('admin.instances.egressGateway')"
                value="gateway"
              />
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('admin.instances.egressTunnelInterface')">
            <el-input
              v-model="form.tunnelInterface"
              maxlength="15"
            />
          </el-form-item>
          <div class="form-grid">
            <el-form-item
              :label="$t('admin.instances.egressRouteTable')"
              :error="routeTableError"
            >
              <el-input-number
                v-model="form.routeTable"
                :min="1"
                :max="MAX_ROUTE_TABLE"
                controls-position="right"
              />
            </el-form-item>
            <el-form-item :label="$t('admin.instances.egressMark')">
              <el-input-number
                v-model="form.mark"
                :min="1"
                :max="MAX_MARK"
                controls-position="right"
              />
            </el-form-item>
          </div>
          <el-form-item :label="$t('admin.instances.egressGateway')">
            <el-input
              v-model="form.gateway"
              clearable
            />
          </el-form-item>
          <div class="form-grid">
            <el-form-item
              :label="$t('admin.instances.publicIPv4')"
              :error="publicIPv4Error"
            >
              <el-input
                v-model="form.publicIPv4"
                clearable
              />
            </el-form-item>
            <el-form-item
              :label="$t('admin.instances.publicIPv6')"
              :error="publicIPv6Error"
            >
              <el-input
                v-model="form.publicIPv6"
                clearable
              />
            </el-form-item>
          </div>
          <el-form-item :label="$t('admin.instances.egressFailClosed')">
            <el-switch
              :model-value="true"
              disabled
            />
          </el-form-item>

          <template v-if="form.tunnelType === 'wireguard'">
            <el-divider content-position="left">
              WireGuard
            </el-divider>
            <el-form-item :label="$t('admin.instances.egressManagedTunnel')">
              <el-switch v-model="form.wireguard.managed" />
            </el-form-item>
            <template v-if="form.wireguard.managed">
              <el-form-item :label="$t('admin.instances.egressPrivateKey')">
                <el-input
                  v-model="form.wireguard.privateKey"
                  type="password"
                  show-password
                  autocomplete="new-password"
                />
                <el-tag
                  v-if="selectedProfile?.wireguard?.private_key_configured"
                  size="small"
                  type="success"
                  class="field-status"
                >
                  {{ $t('admin.instances.egressSecretConfigured') }}
                </el-tag>
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressPeerPublicKey')">
                <el-input v-model="form.wireguard.peerPublicKey" />
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressPresharedKey')">
                <el-input
                  v-model="form.wireguard.presharedKey"
                  type="password"
                  show-password
                  autocomplete="new-password"
                />
                <el-tag
                  v-if="selectedProfile?.wireguard?.preshared_key_configured"
                  size="small"
                  type="success"
                  class="field-status"
                >
                  {{ $t('admin.instances.egressSecretConfigured') }}
                </el-tag>
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressPeerEndpoint')">
                <el-input v-model="form.wireguard.endpoint" />
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressTunnelAddresses')">
                <el-input
                  v-model="form.wireguard.addresses"
                  type="textarea"
                  :rows="2"
                />
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressAllowedIps')">
                <el-input
                  v-model="form.wireguard.allowedIPs"
                  type="textarea"
                  :rows="2"
                />
              </el-form-item>
              <div class="form-grid">
                <el-form-item :label="$t('admin.instances.egressKeepalive')">
                  <el-input-number
                    v-model="form.wireguard.keepalive"
                    :min="0"
                    :max="65535"
                    controls-position="right"
                  />
                </el-form-item>
                <el-form-item label="MTU">
                  <el-input-number
                    v-model="form.wireguard.mtu"
                    :min="576"
                    :max="9000"
                    controls-position="right"
                  />
                </el-form-item>
              </div>
            </template>
          </template>

          <el-collapse class="advanced-fields">
            <el-collapse-item
              :title="$t('admin.instances.egressAdvanced')"
              name="advanced"
            >
              <el-form-item :label="$t('admin.instances.egressSource')">
                <el-input
                  v-model="form.source"
                  :placeholder="$t('admin.instances.egressAutomatic')"
                  clearable
                />
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressSources')">
                <el-input
                  v-model="form.sources"
                  type="textarea"
                  :rows="2"
                  :placeholder="$t('admin.instances.egressSourcesPlaceholder')"
                  clearable
                />
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressHostInterfaceV4')">
                <el-input
                  v-model="form.interfaceV4"
                  :placeholder="$t('admin.instances.egressAutomatic')"
                  clearable
                />
              </el-form-item>
              <el-form-item :label="$t('admin.instances.egressHostInterfaceV6')">
                <el-input
                  v-model="form.interfaceV6"
                  :placeholder="$t('admin.instances.egressAutomatic')"
                  clearable
                />
              </el-form-item>
            </el-collapse-item>
          </el-collapse>
        </el-form>
      </section>
    </div>

    <template #footer>
      <div class="dialog-footer">
        <div class="footer-tools">
          <el-button
            v-if="status && !status.agent_installed"
            :loading="deploying"
            @click="handleDeployAgent"
          >
            <el-icon><Tools /></el-icon>
            {{ $t('admin.instances.egressInstallAgent') }}
          </el-button>
          <el-button
            v-if="status?.capabilities?.missing_dependencies?.length"
            :disabled="!status?.capabilities?.auto_install_enabled"
            :loading="installingDependencies"
            @click="handleEnsureDependencies"
          >
            <el-icon><Tools /></el-icon>
            {{ $t('admin.instances.egressInstallDependencies') }}
          </el-button>
          <el-button
            v-if="status?.binding"
            :loading="reconciling"
            @click="handleReconcile"
          >
            <el-icon><Refresh /></el-icon>
            {{ $t('admin.instances.egressReconcile') }}
          </el-button>
          <el-button
            v-if="status?.binding"
            type="danger"
            plain
            :loading="unbinding"
            @click="handleUnbind"
          >
            <el-icon><Delete /></el-icon>
            {{ $t('admin.instances.egressUnbind') }}
          </el-button>
        </div>
        <div class="footer-actions">
          <el-button @click="$emit('update:modelValue', false)">
            {{ $t('common.cancel') }}
          </el-button>
          <el-button
            type="primary"
            :loading="saving"
            :disabled="!canSave"
            @click="handleSave"
          >
            <el-icon><Connection /></el-icon>
            {{ $t('admin.instances.egressSaveApply') }}
          </el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Connection, Delete, Refresh, Tools } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import {
  bindInstanceEgress,
  deployAgent,
  ensureInstanceEgressDependencies,
  getInstanceEgress,
  reconcileInstanceEgress,
  unbindInstanceEgress
} from '@/api/admin'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  instance: { type: Object, default: null }
})

const emit = defineEmits(['update:modelValue', 'updated'])
const { t, locale } = useI18n()

const loading = ref(false)
const saving = ref(false)
const unbinding = ref(false)
const reconciling = ref(false)
const deploying = ref(false)
const installingDependencies = ref(false)
const status = ref(null)
const selectedProfileId = ref('')
const MIN_AUTO_ROUTE_TABLE = 256
const MAX_ROUTE_TABLE = 9999
const MAX_MARK = 0x00ffffff

const defaultForm = () => ({
  profileId: '',
  mode: 'native',
  tunnelType: 'wireguard',
  tunnelInterface: 'wg-egress0',
  gateway: '',
  routeTable: 1000,
  mark: 100001,
  publicIPv4: '',
  publicIPv6: '',
  source: '',
  sources: '',
  interface: '',
  interfaceV4: '',
  interfaceV6: '',
  wireguard: {
    managed: true,
    privateKey: '',
    peerPublicKey: '',
    presharedKey: '',
    endpoint: '',
    addresses: '',
    allowedIPs: '0.0.0.0/0, ::/0',
    keepalive: 25,
    mtu: 1420
  }
})

const form = reactive(defaultForm())

const modeOptions = computed(() => [
  { label: t('admin.instances.egressModeNative'), value: 'native' },
  { label: t('admin.instances.egressModeGateway'), value: 'gateway' },
  { label: 'CNI', value: 'cni' }
])

const profiles = computed(() => status.value?.profiles || [])
const selectedProfile = computed(() => profiles.value.find(item => item.id === selectedProfileId.value) || null)
const expectedEgress = computed(() => [status.value?.expected_ipv4, status.value?.expected_ipv6].filter(Boolean).join(' / '))
const effectiveEgress = computed(() => [status.value?.effective_ipv4, status.value?.effective_ipv6].filter(Boolean).join(' / '))
const displayedEgress = computed(() => status.value?.effective_verified ? effectiveEgress.value : expectedEgress.value)
const bindingInterfaceV4 = computed(() => status.value?.binding?.interface_v4 || status.value?.binding?.interface || '')
const bindingInterfaceV6 = computed(() => status.value?.binding?.interface_v6 || status.value?.binding?.interface || '')
const requiredSourceFamilies = computed(() => {
  const required = { ipv4: false, ipv6: false }
  const values = `${form.sources || ''}\n${form.source || ''}`
    .split(/[\n,]+/)
    .map(item => item.trim())
    .filter(Boolean)
  values.forEach(value => {
    if (value.includes(':')) required.ipv6 = true
    else required.ipv4 = true
  })

  const networkType = String(props.instance?.networkType || '').toLowerCase()
  if (['nat_ipv4', 'dedicated_ipv4'].includes(networkType)) required.ipv4 = true
  if (['nat_ipv4_ipv6', 'dedicated_ipv4_ipv6', 'no_port_mapping'].includes(networkType)) {
    if (props.instance?.privateIP || props.instance?.publicIP) required.ipv4 = true
    if (props.instance?.ipv6Address || props.instance?.publicIPv6) required.ipv6 = true
  }
  if (networkType === 'ipv6_only') required.ipv6 = true
  return required
})
const publicIPv4Error = computed(() => (
  form.mode === 'native' && requiredSourceFamilies.value.ipv4 && !String(form.publicIPv4 || '').trim()
    ? t('admin.instances.egressExpectedIPv4Required')
    : ''
))
const publicIPv6Error = computed(() => (
  form.mode === 'native' && requiredSourceFamilies.value.ipv6 && !String(form.publicIPv6 || '').trim()
    ? t('admin.instances.egressExpectedIPv6Required')
    : ''
))
const routeTableError = computed(() => {
  const value = Number(form.routeTable)
  if (!Number.isInteger(value) || value < 1 || value > MAX_ROUTE_TABLE || (value >= 253 && value <= 255)) {
    return t('admin.instances.egressRouteTableInvalid', { max: MAX_ROUTE_TABLE })
  }
  return ''
})
const canSave = computed(() => {
  if (!status.value?.agent_installed || status.value?.agent_error) return false
  if (form.mode !== 'native') return false
  if (!form.profileId || !form.tunnelInterface || !form.routeTable || !form.mark) return false
  if (routeTableError.value || publicIPv4Error.value || publicIPv6Error.value || form.mark > MAX_MARK) return false
  return status.value?.native_supported !== false
})

const trafficUpdatedAt = computed(() => {
  const value = status.value?.traffic?.last_sync_at
  if (value) return new Date(value).toLocaleString(locale.value)
  const timestamp = status.value?.traffic?.updated_at
  return timestamp ? formatUnixTime(timestamp) : t('admin.instances.notSet')
})

const resetForm = () => {
  Object.assign(form, defaultForm())
  const id = Number(props.instance?.id || 1)
  form.profileId = `egress-${props.instance?.providerId || 0}-${id}`
  const seed = Math.max(1, Number(props.instance?.providerId || 0) * 131 + id)
  form.routeTable = MIN_AUTO_ROUTE_TABLE + (seed % (MAX_ROUTE_TABLE - MIN_AUTO_ROUTE_TABLE + 1))
  form.mark = 1 + (seed % MAX_MARK)
  selectedProfileId.value = ''
}

const nextAvailableValue = (used, min, max, seed) => {
  const size = max - min + 1
  for (let offset = 0; offset < size; offset += 1) {
    const value = min + ((seed - min + offset + size) % size)
    if (!used.has(value)) return value
  }
  return min
}

const assignAvailablePolicyIds = () => {
  const usedTables = new Set(profiles.value.map(item => Number(item.route_table)).filter(Number.isInteger))
  const usedMarks = new Set(profiles.value.map(item => Number(item.mark)).filter(Number.isInteger))
  form.routeTable = nextAvailableValue(usedTables, MIN_AUTO_ROUTE_TABLE, MAX_ROUTE_TABLE, form.routeTable)
  form.mark = nextAvailableValue(usedMarks, 1, MAX_MARK, form.mark)
}

const splitList = (value) => String(value || '')
  .split(/[\n,]+/)
  .map(item => item.trim())
  .filter(Boolean)

const nullable = value => {
  const cleaned = String(value || '').trim()
  return cleaned || null
}

const apiErrorMessage = error => error?.response?.data?.message || error?.response?.data?.msg || error?.message || t('common.operationFailed')

const fillProfile = (profile) => {
  if (!profile) return
  selectedProfileId.value = profile.id
  form.profileId = profile.id
  form.mode = profile.mode || 'native'
  form.tunnelType = profile.tunnel_type || 'wireguard'
  form.tunnelInterface = profile.tunnel_interface || 'wg-egress0'
  form.gateway = profile.gateway || ''
  form.routeTable = Number(profile.route_table || form.routeTable)
  form.mark = Number(profile.mark || form.mark)
  form.publicIPv4 = profile.public_ipv4 || ''
  form.publicIPv6 = profile.public_ipv6 || ''
  const wg = profile.wireguard
  if (wg) {
    form.wireguard.managed = wg.managed !== false
    form.wireguard.peerPublicKey = wg.peer_public_key || ''
    form.wireguard.endpoint = wg.endpoint || ''
    form.wireguard.addresses = (wg.addresses || []).join(', ')
    form.wireguard.allowedIPs = (wg.allowed_ips || []).join(', ')
    form.wireguard.keepalive = Number(wg.persistent_keepalive ?? 25)
    form.wireguard.mtu = Number(wg.mtu ?? 1420)
    form.wireguard.privateKey = ''
    form.wireguard.presharedKey = ''
  }
}

const selectProfile = profileId => {
  fillProfile(profiles.value.find(item => item.id === profileId))
}

const applyStatusToForm = () => {
  const binding = status.value?.binding
  const profileId = binding?.profile_id || status.value?.configured_profile_id
  const profile = profiles.value.find(item => item.id === profileId)
  if (profile) fillProfile(profile)
  if (binding) {
    form.source = binding.source || ''
    form.sources = (binding.sources || []).join(', ')
    form.interface = binding.interface || ''
    form.interfaceV4 = binding.interface_v4 || binding.interface || ''
    form.interfaceV6 = binding.interface_v6 || binding.interface || ''
  }
  if (!profile) assignAvailablePolicyIds()
  if (!profile && status.value?.native_supported === false && status.value?.recommended_mode) {
    form.mode = status.value.recommended_mode
  }
}

const loadStatus = async () => {
  if (!props.instance?.id) return
  loading.value = true
  try {
    const response = await getInstanceEgress(props.instance.id)
    status.value = response.data || null
    applyStatusToForm()
  } catch (error) {
    status.value = null
    ElMessage.error(apiErrorMessage(error))
  } finally {
    loading.value = false
  }
}

const wireGuardPayload = () => {
  if (form.tunnelType !== 'wireguard' || !form.wireguard.managed) return undefined
  const payload = {
    managed: true,
    private_key: String(form.wireguard.privateKey || '').trim() || undefined,
    peer_public_key: String(form.wireguard.peerPublicKey || '').trim() || undefined,
    preshared_key: String(form.wireguard.presharedKey || '').trim() || undefined,
    endpoint: String(form.wireguard.endpoint || '').trim() || undefined,
    addresses: splitList(form.wireguard.addresses),
    allowed_ips: splitList(form.wireguard.allowedIPs),
    persistent_keepalive: Number(form.wireguard.keepalive || 0),
    mtu: Number(form.wireguard.mtu || 1420)
  }
  return Object.fromEntries(Object.entries(payload).filter(([, value]) => value !== undefined))
}

const bindPayload = () => {
  const sources = splitList(form.sources)
  const source = String(form.source || '').trim() || sources[0] || ''
  return {
    profile: {
      id: form.profileId.trim(),
      mode: form.mode,
      tunnel_type: form.tunnelType,
      tunnel_interface: form.tunnelInterface.trim(),
      gateway: nullable(form.gateway),
      route_table: Number(form.routeTable),
      mark: Number(form.mark),
      public_ipv4: nullable(form.publicIPv4),
      public_ipv6: nullable(form.publicIPv6),
      enabled: true,
      fail_closed: true,
      wireguard: wireGuardPayload()
    },
    source,
    sources,
    interface: nullable(form.interface),
    interface_v4: nullable(form.interfaceV4),
    interface_v6: nullable(form.interfaceV6),
    enabled: true,
    apply: true
  }
}

const handleEnsureDependencies = async () => {
  if (!props.instance?.id) return
  installingDependencies.value = true
  try {
    const packageSet = form.tunnelType === 'wireguard' ? 'wireguard' : 'native'
    const response = await ensureInstanceEgressDependencies(props.instance.id, packageSet)
    ElMessage.success(response.msg || t('admin.instances.egressDependenciesReady'))
    await loadStatus()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  } finally {
    installingDependencies.value = false
  }
}

const handleSave = async () => {
  if (!props.instance?.id) return
  saving.value = true
  try {
    const capabilities = status.value?.capabilities
    if (capabilities?.missing_dependencies?.length) {
      if (!capabilities.auto_install_enabled) {
        throw new Error(t('admin.instances.egressAutoInstallDisabled'))
      }
      const packageSet = form.tunnelType === 'wireguard' ? 'wireguard' : 'native'
      await ensureInstanceEgressDependencies(props.instance.id, packageSet)
    }
    const response = await bindInstanceEgress(props.instance.id, bindPayload())
    const reconcile = response.data?.reconcile
    if (reconcile?.applied) {
      ElMessage.success(response.msg || t('admin.instances.egressApplied'))
    } else if (reconcile?.fail_closed) {
      ElMessage.warning(response.msg || t('admin.instances.egressBlockedFailClosed'))
    } else {
      ElMessage.warning(response.msg || t('admin.instances.egressSavedPending'))
    }
    form.wireguard.privateKey = ''
    form.wireguard.presharedKey = ''
    await loadStatus()
    emit('updated')
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  } finally {
    saving.value = false
  }
}

const handleUnbind = async () => {
  try {
    await ElMessageBox.confirm(
      t('admin.instances.egressUnbindConfirm'),
      t('admin.instances.egressUnbind'),
      { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
    unbinding.value = true
    const response = await unbindInstanceEgress(props.instance.id, true)
    ElMessage.success(response.msg || t('admin.instances.egressUnbindSuccess'))
    resetForm()
    await loadStatus()
    emit('updated')
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(apiErrorMessage(error))
  } finally {
    unbinding.value = false
  }
}

const handleReconcile = async () => {
  reconciling.value = true
  try {
    const response = await reconcileInstanceEgress(props.instance.id, true)
    if (response.data?.reconcile?.applied) {
      ElMessage.success(response.msg || t('admin.instances.egressApplied'))
    } else {
      ElMessage.warning(response.msg || t('admin.instances.egressBlockedFailClosed'))
    }
    await loadStatus()
  } catch (error) {
    ElMessage.error(apiErrorMessage(error))
  } finally {
    reconciling.value = false
  }
}

const handleDeployAgent = async () => {
  try {
    await ElMessageBox.confirm(
      t('admin.instances.egressInstallAgentConfirm'),
      t('admin.instances.egressInstallAgent'),
      { type: 'info', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
    deploying.value = true
    const response = await deployAgent(props.instance.providerId)
    ElMessage.success(response.msg || t('admin.instances.egressAgentTaskCreated'))
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(apiErrorMessage(error))
  } finally {
    deploying.value = false
  }
}

const formatBytes = value => {
  const bytes = Number(value || 0)
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let current = bytes / 1024
  let index = 0
  while (current >= 1024 && index < units.length - 1) {
    current /= 1024
    index += 1
  }
  return `${current.toFixed(2)} ${units[index]}`
}

const formatUnixTime = value => new Date(Number(value) * 1000).toLocaleString(locale.value)

const stateTagType = state => {
  if (state === 'active' || state === 'applied') return 'success'
  if (state === 'blocked' || state === 'error') return 'danger'
  return 'warning'
}

watch(
  () => props.modelValue,
  visible => {
    if (!visible) return
    resetForm()
    loadStatus()
  }
)
</script>

<style scoped>
.egress-dialog {
  min-height: 220px;
}

.egress-dialog > .el-alert + .el-alert {
  margin-top: 8px;
}

.egress-section {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.section-heading {
  min-height: 32px;
  margin-bottom: 10px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 15px;
  font-weight: 600;
}

.inline-detail {
  margin-left: 8px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.form-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
}

.form-grid :deep(.el-input-number) {
  width: 100%;
}

.field-status {
  margin-top: 6px;
}

.advanced-fields {
  margin-top: 10px;
}

.dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.footer-tools,
.footer-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.footer-tools .el-button,
.footer-actions .el-button {
  margin: 0;
}

@media (max-width: 720px) {
  .form-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 0;
  }

  .dialog-footer,
  .footer-tools,
  .footer-actions {
    width: 100%;
  }

  .footer-actions .el-button {
    flex: 1;
  }

  :deep(.el-form-item) {
    display: block;
  }

  :deep(.el-form-item__label) {
    width: 100% !important;
    justify-content: flex-start;
  }

  :deep(.el-form-item__content) {
    margin-left: 0 !important;
  }
}
</style>

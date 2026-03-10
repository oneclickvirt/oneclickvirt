<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <el-divider content-position="left">
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.portMappingConfig') }}</span>
    </el-divider>

    <el-form-item
      :label="$t('admin.providers.defaultPortCount')"
      prop="defaultPortCount"
    >
      <el-input-number
        v-model="modelValue.defaultPortCount"
        :min="1"
        :max="50"
        :step="1"
        :controls="false"
        placeholder="10"
        style="width: 200px"
      />
    </el-form-item>
    <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.defaultPortCountTip') }}
      </el-text>
    </div>

    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.portRangeStart')"
          prop="portRangeStart"
        >
          <el-input-number
            v-model="modelValue.portRangeStart"
            :min="1024"
            :max="65535"
            :step="1"
            :controls="false"
            placeholder="10000"
            style="width: 100%"
          />
        </el-form-item>
        <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
          <el-text
            size="small"
            type="info"
          >
            {{ $t('admin.providers.portRangeStartTip') }}
          </el-text>
        </div>
      </el-col>
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.portRangeEnd')"
          prop="portRangeEnd"
        >
          <el-input-number
            v-model="modelValue.portRangeEnd"
            :min="1024"
            :max="65535"
            :step="1"
            :controls="false"
            placeholder="65535"
            style="width: 100%"
          />
        </el-form-item>
        <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
          <el-text
            size="small"
            type="info"
          >
            {{ $t('admin.providers.portRangeEndTip') }}
          </el-text>
        </div>
      </el-col>
    </el-row>

    <el-form-item
      :label="$t('admin.providers.networkType')"
      prop="networkType"
    >
      <el-select
        v-model="modelValue.networkType"
        :placeholder="$t('admin.providers.networkTypePlaceholder')"
        style="width: 100%"
      >
        <el-option
          :label="$t('admin.providers.natIPv4')"
          value="nat_ipv4"
        />
        <el-option
          :label="$t('admin.providers.natIPv4IPv6')"
          value="nat_ipv4_ipv6"
        />
        <el-option
          :label="$t('admin.providers.dedicatedIPv4')"
          value="dedicated_ipv4"
        />
        <el-option
          :label="$t('admin.providers.dedicatedIPv4IPv6')"
          value="dedicated_ipv4_ipv6"
        />
        <el-option
          :label="$t('admin.providers.ipv6Only')"
          value="ipv6_only"
        />
      </el-select>
    </el-form-item>
    <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.networkTypeTip') }}
      </el-text>
    </div>

    <!-- Docker/Podman/Containerd 端口映射方式（固定为 native，不可选择） -->
    <el-form-item
      v-if="['docker', 'podman', 'containerd'].includes(modelValue.type)"
      :label="$t('admin.providers.portMappingMethod')"
    >
      <el-input
        value="Native（原生）"
        disabled
        style="width: 100%"
      />
    </el-form-item>
    <div v-if="['docker', 'podman', 'containerd'].includes(modelValue.type)" class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.dockerNativeMappingTip') }}
      </el-text>
    </div>

    <!-- IPv4端口映射方式 -->
    <el-form-item
      v-if="(modelValue.type === 'lxd' || modelValue.type === 'incus') && modelValue.networkType !== 'ipv6_only'"
      :label="$t('admin.providers.ipv4PortMappingMethod')"
      prop="ipv4PortMappingMethod"
    >
      <el-select
        v-model="modelValue.ipv4PortMappingMethod"
        :placeholder="$t('admin.providers.ipv4PortMappingMethodPlaceholder')"
        style="width: 100%"
      >
        <el-option
          :label="$t('admin.providers.deviceProxyRecommended')"
          value="device_proxy"
        />
        <el-option
          label="Iptables"
          value="iptables"
        />
      </el-select>
    </el-form-item>
    <div v-if="(modelValue.type === 'lxd' || modelValue.type === 'incus') && modelValue.networkType !== 'ipv6_only'" class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.ipv4PortMappingMethodTip') }}
      </el-text>
    </div>

    <!-- IPv6端口映射方式 -->
    <el-form-item
      v-if="(modelValue.type === 'lxd' || modelValue.type === 'incus') && (modelValue.networkType === 'nat_ipv4_ipv6' || modelValue.networkType === 'dedicated_ipv4_ipv6' || modelValue.networkType === 'ipv6_only')"
      :label="$t('admin.providers.ipv6PortMappingMethod')"
      prop="ipv6PortMappingMethod"
    >
      <el-select
        v-model="modelValue.ipv6PortMappingMethod"
        :placeholder="$t('admin.providers.ipv6PortMappingMethodPlaceholder')"
        style="width: 100%"
      >
        <el-option
          :label="$t('admin.providers.deviceProxyRecommended')"
          value="device_proxy"
        />
        <el-option
          label="Iptables"
          value="iptables"
        />
      </el-select>
    </el-form-item>
    <div v-if="(modelValue.type === 'lxd' || modelValue.type === 'incus') && (modelValue.networkType === 'nat_ipv4_ipv6' || modelValue.networkType === 'dedicated_ipv4_ipv6' || modelValue.networkType === 'ipv6_only')" class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.ipv6PortMappingMethodTip') }}
      </el-text>
    </div>

    <!-- Proxmox IPv4端口映射方式 -->
    <el-form-item
      v-if="modelValue.type === 'proxmox' && modelValue.networkType !== 'ipv6_only'"
      :label="$t('admin.providers.ipv4PortMappingMethod')"
      prop="ipv4PortMappingMethod"
    >
      <el-select
        v-model="modelValue.ipv4PortMappingMethod"
        :placeholder="$t('admin.providers.ipv4PortMappingMethodPlaceholder')"
        style="width: 100%"
      >
        <el-option
          v-if="modelValue.networkType === 'dedicated_ipv4' || modelValue.networkType === 'dedicated_ipv4_ipv6'"
          :label="$t('admin.providers.nativeRecommended')"
          value="native"
        />
        <el-option
          label="Iptables"
          value="iptables"
        />
      </el-select>
    </el-form-item>
    <div v-if="modelValue.type === 'proxmox' && modelValue.networkType !== 'ipv6_only'" class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.proxmoxIPv4MappingTip') }}
      </el-text>
    </div>

    <!-- Proxmox IPv6端口映射方式 -->
    <el-form-item
      v-if="modelValue.type === 'proxmox' && (modelValue.networkType === 'nat_ipv4_ipv6' || modelValue.networkType === 'dedicated_ipv4_ipv6' || modelValue.networkType === 'ipv6_only')"
      :label="$t('admin.providers.ipv6PortMappingMethod')"
      prop="ipv6PortMappingMethod"
    >
      <el-select
        v-model="modelValue.ipv6PortMappingMethod"
        :placeholder="$t('admin.providers.ipv6PortMappingMethodPlaceholder')"
        style="width: 100%"
      >
        <el-option
          :label="$t('admin.providers.nativeRecommended')"
          value="native"
        />
        <el-option
          label="Iptables"
          value="iptables"
        />
      </el-select>
    </el-form-item>
    <div v-if="modelValue.type === 'proxmox' && (modelValue.networkType === 'nat_ipv4_ipv6' || modelValue.networkType === 'dedicated_ipv4_ipv6' || modelValue.networkType === 'ipv6_only')" class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.proxmoxIPv6MappingTip') }}
      </el-text>
    </div>

    <el-alert
      :title="$t('admin.providers.mappingTypeDescription')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-top: 20px;"
    >
      <ul style="margin: 0; padding-left: 20px;">
        <li><strong>{{ $t('admin.providers.natMapping') }}:</strong> {{ $t('admin.providers.natMappingDesc') }}</li>
        <li><strong>{{ $t('admin.providers.dedicatedMapping') }}:</strong> {{ $t('admin.providers.dedicatedMappingDesc') }}</li>
        <li><strong>{{ $t('admin.providers.ipv6Support') }}:</strong> {{ $t('admin.providers.ipv6SupportDesc') }}</li>
        <li><strong>Docker:</strong> {{ $t('admin.providers.dockerMappingDesc') }}</li>
        <li><strong>LXD/Incus:</strong> {{ $t('admin.providers.lxdIncusMappingDesc') }}</li>
        <li><strong>Proxmox VE:</strong> {{ $t('admin.providers.proxmoxMappingDesc') }}</li>
      </ul>
    </el-alert>

    <!-- IPv4 地址池管理（仅对 dedicated_ipv4 / dedicated_ipv4_ipv6 显示） -->
    <template v-if="modelValue.networkType === 'dedicated_ipv4' || modelValue.networkType === 'dedicated_ipv4_ipv6'">
      <el-divider content-position="left" style="margin-top: 24px;">
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
        <el-row :gutter="16" style="margin-bottom: 16px;">
          <el-col :span="8">
            <el-statistic :title="$t('admin.providers.ipv4Pool.total')" :value="poolStats.total" />
          </el-col>
          <el-col :span="8">
            <el-statistic :title="$t('admin.providers.ipv4Pool.allocated')" :value="poolStats.allocated" />
          </el-col>
          <el-col :span="8">
            <el-statistic :title="$t('admin.providers.ipv4Pool.available')" :value="poolStats.available" />
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
                  <el-button type="danger" plain>{{ $t('admin.providers.ipv4Pool.clearBtn') }}</el-button>
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
                <el-tag :type="row.is_allocated ? 'warning' : 'success'" size="small">
                  {{ row.is_allocated ? $t('admin.providers.ipv4Pool.statusAllocated') : $t('admin.providers.ipv4Pool.statusFree') }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('admin.providers.ipv4Pool.instance')"
              prop="instance_id"
              min-width="90"
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
                    <el-button type="danger" link size="small">{{ $t('common.delete') }}</el-button>
                  </template>
                </el-popconfirm>
              </template>
            </el-table-column>
          </el-table>
        </el-form-item>
      </template>
    </template>
  </el-form>
</template>

<script setup>
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getProviderIPv4Pool, setProviderIPv4Pool, clearProviderIPv4Pool, deleteProviderIPv4PoolEntry } from '@/api/admin'

const props = defineProps({
  modelValue: {
    type: Object,
    required: true
  }
})

const { t } = useI18n()

// ---- IPv4 地址池状态 ----
const poolEntries = ref([])
const poolStats = ref({ total: 0, allocated: 0, available: 0 })
const poolLoading = ref(false)
const newAddresses = ref('')
const saving = ref(false)

async function loadPool() {
  if (!props.modelValue.id) return
  poolLoading.value = true
  try {
    const res = await getProviderIPv4Pool(props.modelValue.id, { page: 1, page_size: 200 })
    if (res.data) {
      poolEntries.value = res.data.list || []
      poolStats.value = res.data.stats || { total: 0, allocated: 0, available: 0 }
    }
  } catch {
    ElMessage.error(t('admin.providers.ipv4Pool.loadFailed'))
  } finally {
    poolLoading.value = false
  }
}

async function addToPool() {
  if (!newAddresses.value.trim()) return
  saving.value = true
  try {
    await setProviderIPv4Pool(props.modelValue.id, { addresses: newAddresses.value })
    ElMessage.success(t('admin.providers.ipv4Pool.addSuccess'))
    newAddresses.value = ''
    await loadPool()
  } catch {
    ElMessage.error(t('admin.providers.ipv4Pool.addFailed'))
  } finally {
    saving.value = false
  }
}

async function clearPool() {
  try {
    await clearProviderIPv4Pool(props.modelValue.id)
    ElMessage.success(t('admin.providers.ipv4Pool.clearSuccess'))
    await loadPool()
  } catch {
    ElMessage.error(t('admin.providers.ipv4Pool.loadFailed'))
  }
}

async function deleteEntry(entryId) {
  try {
    await deleteProviderIPv4PoolEntry(props.modelValue.id, entryId)
    ElMessage.success(t('admin.providers.ipv4Pool.deleteSuccess'))
    await loadPool()
  } catch {
    ElMessage.error(t('admin.providers.ipv4Pool.loadFailed'))
  }
}

// 当提供商 ID 变更（首次登载 / 的切换编辑）时重收pool
watch(() => props.modelValue.id, (id) => {
  if (id) loadPool()
}, { immediate: true })

// 当 networkType 切换为 dedicated_ipv4* 且已有 id 时加载
watch(() => props.modelValue.networkType, (nt) => {
  if ((nt === 'dedicated_ipv4' || nt === 'dedicated_ipv4_ipv6') && props.modelValue.id) {
    loadPool()
  }
})

// 监听节点类型变化，自动更新端口映射方式
watch(() => props.modelValue.type, (newType) => {
  if (!newType) return
  
  if (['docker', 'podman', 'containerd'].includes(newType)) {
    // Docker/Podman/Containerd: IPv4和IPv6都固定使用 native
    props.modelValue.ipv4PortMappingMethod = 'native'
    props.modelValue.ipv6PortMappingMethod = 'native'
  } else if (newType === 'proxmox') {
    // Proxmox: 根据网络类型设置
    const isNATMode = props.modelValue.networkType === 'nat_ipv4' || props.modelValue.networkType === 'nat_ipv4_ipv6'
    // IPv4: NAT模式默认iptables，独立IP模式默认native
    props.modelValue.ipv4PortMappingMethod = isNATMode ? 'iptables' : 'native'
    // IPv6: 默认native（Proxmox IPv6始终推荐native）
    props.modelValue.ipv6PortMappingMethod = 'native'
  } else if (newType === 'lxd' || newType === 'incus') {
    // LXD/Incus: IPv4和IPv6都默认使用 device_proxy
    props.modelValue.ipv4PortMappingMethod = 'device_proxy'
    props.modelValue.ipv6PortMappingMethod = 'device_proxy'
  }
})

// 监听网络类型变化，自动调整端口映射方式
watch(() => [props.modelValue.type, props.modelValue.networkType], ([type, networkType]) => {
  if (!type || !networkType) return
  
  if (type === 'proxmox') {
    const isNATMode = networkType === 'nat_ipv4' || networkType === 'nat_ipv4_ipv6'
    const isDedicatedIPv4Mode = networkType === 'dedicated_ipv4' || networkType === 'dedicated_ipv4_ipv6'
    const hasIPv6 = networkType === 'nat_ipv4_ipv6' || networkType === 'dedicated_ipv4_ipv6' || networkType === 'ipv6_only'
    
    // IPv4 端口映射方式处理（仅在网络类型支持IPv4时处理）
    if (networkType !== 'ipv6_only') {
      if (isNATMode) {
        // NAT 模式只能使用 iptables
        props.modelValue.ipv4PortMappingMethod = 'iptables'
      } else if (isDedicatedIPv4Mode) {
        // 独立IP模式：如果当前值不是有效选项（native或iptables），则设为native
        if (props.modelValue.ipv4PortMappingMethod !== 'native' && 
            props.modelValue.ipv4PortMappingMethod !== 'iptables') {
          props.modelValue.ipv4PortMappingMethod = 'native'
        }
      }
    }
    
    // IPv6 端口映射方式处理（仅在网络类型支持IPv6时处理）
    if (hasIPv6) {
      // Proxmox IPv6默认使用native，但也支持iptables
      if (props.modelValue.ipv6PortMappingMethod !== 'native' && 
          props.modelValue.ipv6PortMappingMethod !== 'iptables') {
        props.modelValue.ipv6PortMappingMethod = 'native'
      }
    }
  }
  // LXD/Incus不需要额外处理，它们的IPv4和IPv6都是device_proxy或iptables
  // Docker不需要额外处理，它们固定是native
})
</script>

<style scoped>
.server-form {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

.form-tip {
  margin-top: 5px;
}
</style>

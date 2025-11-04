<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <el-alert
      :title="$t('admin.providers.portMappingConfigTitle')"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      {{ $t('admin.providers.portMappingConfigMessage') }}
    </el-alert>

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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.defaultPortCountTip') }}
        </el-text>
      </div>
    </el-form-item>

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
          <div class="form-tip">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.portRangeStartTip') }}
            </el-text>
          </div>
        </el-form-item>
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
          <div class="form-tip">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.portRangeEndTip') }}
            </el-text>
          </div>
        </el-form-item>
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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.networkTypeTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- Docker 端口映射方式（固定为 native，不可选择） -->
    <el-form-item
      v-if="modelValue.type === 'docker'"
      :label="$t('admin.providers.portMappingMethod')"
    >
      <el-input
        value="Native（原生）"
        disabled
        style="width: 100%"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.dockerNativeMappingTip') }}
        </el-text>
      </div>
    </el-form-item>

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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.ipv4PortMappingMethodTip') }}
        </el-text>
      </div>
    </el-form-item>

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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.ipv6PortMappingMethodTip') }}
        </el-text>
      </div>
    </el-form-item>

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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.proxmoxIPv4MappingTip') }}
        </el-text>
      </div>
    </el-form-item>

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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.proxmoxIPv6MappingTip') }}
        </el-text>
      </div>
    </el-form-item>

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
  </el-form>
</template>

<script setup>
defineProps({
  modelValue: {
    type: Object,
    required: true
  }
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

<template>
  <div class="overview-content">
    <!-- SSH连接信息 -->
    <div class="connection-section">
      <h3>{{ $t('user.instanceDetail.sshConnection') }}</h3>
      <div class="connection-grid">
        <div class="connection-item">
          <span class="label">{{ $t('user.instanceDetail.publicIPv4') }}</span>
          <div class="value-with-action">
            <span
              class="value ip-value"
              :title="instance.publicIP || $t('user.instanceDetail.none')"
            >
              {{ truncateIP(instance.publicIP) || $t('user.instanceDetail.none') }}
            </span>
            <el-button
              v-if="instance.publicIP"
              size="small"
              text
              @click="$emit('copy', instance.publicIP)"
            >
              {{ $t('user.instanceDetail.copy') }}
            </el-button>
          </div>
        </div>
        <div
          v-if="instance.privateIP"
          class="connection-item"
        >
          <span class="label">{{ $t('user.instanceDetail.privateIPv4') }}</span>
          <div class="value-with-action">
            <span
              class="value ip-value"
              :title="instance.privateIP"
            >
              {{ truncateIP(instance.privateIP) }}
            </span>
            <el-button
              size="small"
              text
              @click="$emit('copy', instance.privateIP)"
            >
              {{ $t('user.instanceDetail.copy') }}
            </el-button>
          </div>
        </div>
        <div
          v-if="instance.ipv6Address"
          class="connection-item"
        >
          <span class="label">{{ $t('user.instanceDetail.ipv6') }}</span>
          <div class="value-with-action">
            <span
              class="value ip-value"
              :title="instance.ipv6Address"
            >
              {{ truncateIP(instance.ipv6Address) }}
            </span>
            <el-button
              size="small"
              text
              @click="$emit('copy', instance.ipv6Address)"
            >
              {{ $t('user.instanceDetail.copy') }}
            </el-button>
          </div>
        </div>
        <div
          v-if="instance.publicIPv6"
          class="connection-item"
        >
          <span class="label">{{ $t('user.instanceDetail.ipv6') }}</span>
          <div class="value-with-action">
            <span
              class="value ip-value"
              :title="instance.publicIPv6"
            >
              {{ truncateIP(instance.publicIPv6) }}
            </span>
            <el-button
              size="small"
              text
              @click="$emit('copy', instance.publicIPv6)"
            >
              {{ $t('user.instanceDetail.copy') }}
            </el-button>
          </div>
        </div>
        <div class="connection-item">
          <span class="label">{{ $t('user.instanceDetail.sshPort') }}</span>
          <div class="value-with-action">
            <span class="value">{{ instance.sshPort || 22 }}</span>
            <el-button
              v-if="instance.sshPort"
              size="small"
              text
              @click="$emit('copy', instance.sshPort.toString())"
            >
              {{ $t('user.instanceDetail.copy') }}
            </el-button>
          </div>
        </div>
        <div class="connection-item">
          <span class="label">{{ $t('user.instanceDetail.username') }}</span>
          <div class="value-with-action">
            <span class="value">{{ instance.username || 'root' }}</span>
            <el-button
              v-if="instance.username"
              size="small"
              text
              @click="$emit('copy', instance.username)"
            >
              {{ $t('user.instanceDetail.copy') }}
            </el-button>
          </div>
        </div>
        <div
          v-if="instance.password"
          class="connection-item"
        >
          <span class="label">{{ $t('user.instanceDetail.password') }}</span>
          <div class="value-with-action">
            <span class="value">{{ showPassword ? instance.password : '••••••••' }}</span>
            <el-button
              size="small"
              text
              @click="$emit('toggle-password')"
            >
              {{ showPassword ? $t('user.instanceDetail.hide') : $t('user.instanceDetail.show') }}
            </el-button>
            <el-button
              size="small"
              text
              @click="$emit('copy', instance.password)"
            >
              {{ $t('user.instanceDetail.copy') }}
            </el-button>
          </div>
        </div>
      </div>
    </div>

    <!-- 基本信息 -->
    <div class="basic-info-section">
      <h3>{{ $t('user.instanceDetail.basicInfo') }}</h3>
      <div class="info-grid">
        <div class="info-item">
          <span class="label">{{ $t('user.instanceDetail.os') }}</span>
          <span class="value"><OsIcon
            :name="instance.osType || instance.image"
            :size="20"
            style="margin-right: 6px;"
          />{{ instance.osType }}</span>
        </div>
        <div class="info-item">
          <span class="label">{{ $t('user.instanceDetail.createdAt') }}</span>
          <span class="value">{{ formatDate(instance.createdAt) }}</span>
        </div>
        <div class="info-item">
          <span class="label">{{ $t('user.instanceDetail.expiredAt') }}</span>
          <span class="value">{{ formatDate(instance.expiresAt) }}</span>
        </div>
        <div
          v-if="instance.networkType || instance.ipv4MappingType"
          class="info-item"
        >
          <span class="label">{{ $t('user.instanceDetail.networkType') }}</span>
          <el-tag
            size="small"
            :type="getNetworkTypeTagType(instance.networkType || getNetworkTypeFromLegacy(instance.ipv4MappingType, instance.ipv6Address))"
          >
            {{ getNetworkTypeDisplayName(instance.networkType || getNetworkTypeFromLegacy(instance.ipv4MappingType, instance.ipv6Address)) }}
          </el-tag>
        </div>
        <!-- 保留旧字段显示以兼容性 -->
        <div
          v-if="instance.ipv4MappingType && !instance.networkType"
          class="info-item"
          style="display: none"
        >
          <span class="label">{{ $t('user.instanceDetail.ipv4MappingTypeCompat') }}</span>
          <el-tag
            size="small"
            :type="instance.ipv4MappingType === 'dedicated' ? 'success' : 'primary'"
          >
            {{ instance.ipv4MappingType === 'dedicated' ? $t('user.instanceDetail.dedicatedIPv4') : $t('user.instanceDetail.natSharedIP') }}
          </el-tag>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import OsIcon from '@/components/OsIcon.vue'
import { useInstanceFormatters } from '../composables/useInstanceFormatters'

defineProps({
  instance: { type: Object, required: true },
  showPassword: { type: Boolean, default: false }
})

defineEmits(['toggle-password', 'copy'])

const {
  getNetworkTypeFromLegacy,
  getNetworkTypeDisplayName,
  getNetworkTypeTagType,
  formatDate
} = useInstanceFormatters()

const truncateIP = (ip, maxLength = 25) => {
  if (!ip) return ''
  return ip.length > maxLength ? ip.substring(0, maxLength) + '...' : ip
}
</script>

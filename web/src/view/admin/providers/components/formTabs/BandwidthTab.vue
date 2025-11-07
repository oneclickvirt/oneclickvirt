<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <el-alert
      :title="$t('admin.providers.bandwidthConfigTitle')"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      {{ $t('admin.providers.bandwidthConfigDesc') }}
    </el-alert>

    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.defaultInboundBandwidth')"
          prop="defaultInboundBandwidth"
        >
          <el-input-number
            v-model="modelValue.defaultInboundBandwidth"
            :min="1"
            :max="10000"
            :step="50"
            :controls="false"
            placeholder="300"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.defaultInboundBandwidthTip') }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.defaultOutboundBandwidth')"
          prop="defaultOutboundBandwidth"
        >
          <el-input-number
            v-model="modelValue.defaultOutboundBandwidth"
            :min="1"
            :max="10000"
            :step="50"
            :controls="false"
            placeholder="300"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.defaultOutboundBandwidthTip') }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.maxInboundBandwidth')"
          prop="maxInboundBandwidth"
        >
          <el-input-number
            v-model="modelValue.maxInboundBandwidth"
            :min="1"
            :max="10000"
            :step="50"
            :controls="false"
            placeholder="1000"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.maxInboundBandwidthTip') }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.maxOutboundBandwidth')"
          prop="maxOutboundBandwidth"
        >
          <el-input-number
            v-model="modelValue.maxOutboundBandwidth"
            :min="1"
            :max="10000"
            :step="50"
            :controls="false"
            placeholder="1000"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.maxOutboundBandwidthTip') }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <el-divider content-position="left">
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.trafficConfig') }}</span>
    </el-divider>

    <el-form-item
      :label="$t('admin.providers.enableTrafficControl')"
      prop="enableTrafficControl"
    >
      <el-switch
        v-model="modelValue.enableTrafficControl"
        :active-text="$t('admin.providers.enabled')"
        :inactive-text="$t('admin.providers.disabled')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.enableTrafficControlTip') }}
        </el-text>
      </div>
    </el-form-item>

    <el-form-item
      :label="$t('admin.providers.maxTraffic')"
      prop="maxTraffic"
      v-show="modelValue.enableTrafficControl"
    >
      <el-input-number
        v-model="maxTrafficTB"
        :min="0.001"
        :max="10"
        :step="0.1"
        :precision="3"
        :controls="false"
        placeholder="1"
        style="width: 100%"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.maxTrafficTip') }}
        </el-text>
      </div>
    </el-form-item>

    <el-form-item
      :label="$t('admin.providers.trafficCountMode')"
      prop="trafficCountMode"
      v-show="modelValue.enableTrafficControl"
    >
      <el-select
        v-model="modelValue.trafficCountMode"
        :placeholder="$t('admin.providers.selectTrafficCountMode')"
        style="width: 100%"
      >
        <el-option
          :label="$t('admin.providers.trafficCountModeBoth')"
          value="both"
        />
        <el-option
          :label="$t('admin.providers.trafficCountModeOut')"
          value="out"
        />
        <el-option
          :label="$t('admin.providers.trafficCountModeIn')"
          value="in"
        />
      </el-select>
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.trafficCountModeTip') }}
        </el-text>
      </div>
    </el-form-item>

    <el-form-item
      :label="$t('admin.providers.trafficMultiplier')"
      prop="trafficMultiplier"
      v-show="modelValue.enableTrafficControl"
    >
      <el-input-number
        v-model="modelValue.trafficMultiplier"
        :min="0.1"
        :max="10"
        :step="0.1"
        :precision="2"
        :controls="false"
        placeholder="1.0"
        style="width: 100%"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.trafficMultiplierTip') }}
        </el-text>
      </div>
    </el-form-item>

    <el-alert
      :title="$t('admin.providers.bandwidthMechanismTitle')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-top: 20px;"
    >
      <ul style="margin: 0; padding-left: 20px;">
        <li><strong>{{ $t('admin.providers.defaultBandwidth') }}:</strong> {{ $t('admin.providers.defaultBandwidthDesc') }}</li>
        <li><strong>{{ $t('admin.providers.maxBandwidth') }}:</strong> {{ $t('admin.providers.maxBandwidthDesc') }}</li>
        <li><strong>{{ $t('admin.providers.userLevel') }}:</strong> {{ $t('admin.providers.userLevelDesc') }}</li>
        <li><strong>{{ $t('admin.providers.trafficLimit') }}:</strong> {{ $t('admin.providers.trafficLimitDesc') }}</li>
      </ul>
    </el-alert>
  </el-form>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  modelValue: {
    type: Object,
    required: true
  }
})

// 流量单位转换：TB 转 MB (1TB = 1024 * 1024 MB = 1048576 MB)
const TB_TO_MB = 1048576

// 计算属性：maxTraffic 的 TB 单位显示
const maxTrafficTB = computed({
  get: () => {
    // 从 MB 转换为 TB
    return Number((props.modelValue.maxTraffic / TB_TO_MB).toFixed(3))
  },
  set: (value) => {
    // 从 TB 转换为 MB
    props.modelValue.maxTraffic = Math.round(value * TB_TO_MB)
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

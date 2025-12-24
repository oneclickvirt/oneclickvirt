<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <el-divider content-position="left">
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.bandwidthLimits') }}</span>
    </el-divider>

    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.defaultInboundBandwidth')"
          prop="defaultInboundBandwidth"
        >
          <el-input-number
            v-model="modelValue.defaultInboundBandwidth"
            :min="1"
            :max="1000000"
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
            :max="1000000"
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
            :max="1000000"
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
            :max="1000000"
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
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.trafficStatistics') }}</span>
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
        :max="1000"
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

    <el-divider content-position="left" v-show="modelValue.enableTrafficControl">
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.trafficStatsConfig') || '流量统计配置' }}</span>
    </el-divider>

    <el-form-item
      :label="$t('admin.providers.trafficStatsMode') || '统计模式'"
      prop="trafficStatsMode"
      v-show="modelValue.enableTrafficControl"
    >
      <el-select
        v-model="modelValue.trafficStatsMode"
        :placeholder="$t('admin.providers.selectTrafficStatsMode')"
        style="width: 100%"
        @change="handlePresetChange"
      >
        <el-option :label="$t('admin.providers.trafficStatsModeHigh')" value="high" />
        <el-option :label="$t('admin.providers.trafficStatsModeStandard')" value="standard" />
        <el-option :label="$t('admin.providers.trafficStatsModeLight')" value="light" />
        <el-option :label="$t('admin.providers.trafficStatsModeMinimal')" value="minimal" />
        <el-option :label="$t('admin.providers.trafficStatsModeCustom')" value="custom" />
      </el-select>
      <div class="form-tip">
        <el-text size="small" type="info">
          {{ $t('admin.providers.trafficStatsModeTip') || '选择预设的流量统计模式，或选择自定义进行详细配置' }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 流量统计详细配置 - 始终显示，但非自定义模式为只读 -->
    <el-row :gutter="20" v-show="modelValue.enableTrafficControl">
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.trafficCollectInterval') || '采集间隔(秒)'"
          prop="trafficCollectInterval"
        >
          <el-input-number
            v-model="modelValue.trafficCollectInterval"
            :min="30"
            :max="300"
            :step="30"
            :controls="false"
            :disabled="modelValue.trafficStatsMode !== 'custom'"
            placeholder="300"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text size="small" type="info">
              {{ $t('admin.providers.trafficCollectIntervalTip') || '从Provider采集流量数据并同步统计的间隔，最长不超过5分钟（300秒）' }}{{ modelValue.trafficStatsMode !== 'custom' ? '（' + ($t('common.presetValue') || '预设值，不可修改') + '）' : '' }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.trafficCollectBatchSize') || '批量大小'"
          prop="trafficCollectBatchSize"
        >
          <el-input-number
            v-model="modelValue.trafficCollectBatchSize"
            :min="1"
            :max="100"
            :step="5"
            :controls="false"
            :disabled="modelValue.trafficStatsMode !== 'custom'"
            placeholder="10"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text size="small" type="info">
              {{ $t('admin.providers.trafficCollectBatchSizeTip') || '每次采集处理的实例数量' }}{{ modelValue.trafficStatsMode !== 'custom' ? '（' + ($t('common.presetValue') || '预设值，不可修改') + '）' : '' }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <el-row :gutter="20" v-show="modelValue.enableTrafficControl">
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.trafficLimitCheckInterval') || '限制检测间隔(秒)'"
          prop="trafficLimitCheckInterval"
        >
          <el-input-number
            v-model="modelValue.trafficLimitCheckInterval"
            :min="60"
            :max="3600"
            :step="30"
            :controls="false"
            :disabled="modelValue.trafficStatsMode !== 'custom'"
            placeholder="600"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text size="small" type="info">
              {{ $t('admin.providers.trafficLimitCheckIntervalTip') || '检查实例是否超出流量限制的间隔' }}{{ modelValue.trafficStatsMode !== 'custom' ? '（' + ($t('common.presetValue') || '预设值，不可修改') + '）' : '' }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.providers.trafficLimitCheckBatchSize') || '检测批量大小'"
          prop="trafficLimitCheckBatchSize"
        >
          <el-input-number
            v-model="modelValue.trafficLimitCheckBatchSize"
            :min="1"
            :max="100"
            :step="5"
            :controls="false"
            :disabled="modelValue.trafficStatsMode !== 'custom'"
            placeholder="10"
            style="width: 100%"
          />
          <div class="form-tip">
            <el-text size="small" type="info">
              {{ $t('admin.providers.trafficLimitCheckBatchSizeTip') || '每次检测的实例数量' }}{{ modelValue.trafficStatsMode !== 'custom' ? '（' + ($t('common.presetValue') || '预设值，不可修改') + '）' : '' }}
            </el-text>
          </div>
        </el-form-item>
      </el-col>
    </el-row>
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

// 预设配置（与后端保持一致）- 简化版本，只保留实际使用的字段
const presets = {
  high: {
    trafficCollectInterval: 30,  // 0.5分钟采集+统计
    trafficCollectBatchSize: 20,
    trafficLimitCheckInterval: 30,  // 30秒检测
    trafficLimitCheckBatchSize: 20,
    trafficAutoResetInterval: 600,  // 10分钟检查
    trafficAutoResetBatchSize: 20
  },
  standard: {
    trafficCollectInterval: 60,  // 1分钟采集+统计
    trafficCollectBatchSize: 15,
    trafficLimitCheckInterval: 60,  // 1分钟检测
    trafficLimitCheckBatchSize: 15,
    trafficAutoResetInterval: 900,  // 15分钟检查
    trafficAutoResetBatchSize: 15
  },
  light: {
    trafficCollectInterval: 90,   // 1.5分钟采集+统计
    trafficCollectBatchSize: 10,
    trafficLimitCheckInterval: 90,   // 1.5分钟检测
    trafficLimitCheckBatchSize: 10,
    trafficAutoResetInterval: 1800,  // 30分钟检查
    trafficAutoResetBatchSize: 10
  },
  minimal: {
    trafficCollectInterval: 120,  // 2分钟采集+统计
    trafficCollectBatchSize: 5,
    trafficLimitCheckInterval: 120,  // 2分钟检测
    trafficLimitCheckBatchSize: 5,
    trafficAutoResetInterval: 3600,  // 60分钟检查
    trafficAutoResetBatchSize: 5
  }
}

// 处理预设模式变更
const handlePresetChange = (mode) => {
  if (mode !== 'custom' && presets[mode]) {
    Object.assign(props.modelValue, presets[mode])
  }
}
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

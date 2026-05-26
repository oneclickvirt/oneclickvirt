<template>
  <el-form
    v-loading="loading"
    :model="config"
    label-width="140px"
    class="config-form"
  >
    <el-alert
      :title="$t('admin.config.userLevelDesc')"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      <div>{{ $t('admin.config.userLevelHint') }}</div>
      <div style="margin-top: 8px; color: #67C23A;">
        <i class="el-icon-check" />
        {{ $t('admin.config.autoSyncHint') }}
      </div>
      <div style="margin-top: 8px; color: #E6A23C;">
        <i class="el-icon-warning" />
        {{ $t('admin.config.resourceLimitWarning') }}
      </div>
    </el-alert>

    <el-form-item :label="$t('admin.config.newUserDefaultLevel')">
      <el-select
        v-model="config.quota.defaultLevel"
        :placeholder="$t('admin.config.selectDefaultLevel')"
        style="width: 200px"
      >
        <el-option
          v-for="level in 5"
          :key="level"
          :label="$t('admin.config.levelN', { level })"
          :value="level"
        />
      </el-select>
    </el-form-item>

    <el-divider content-position="left">
      {{ $t('admin.config.levelLimitsConfig') }}
    </el-divider>

    <!-- 等级限制配置 -->
    <el-row :gutter="15">
      <el-col
        v-for="level in 5"
        :key="level"
        :span="24"
        style="margin-bottom: 15px;"
      >
        <el-card
          class="level-card"
          :class="{ 'default-level': config.quota.defaultLevel === level }"
          shadow="hover"
        >
          <template #header>
            <div class="level-header">
              <span class="level-title">{{ $t('admin.config.levelNLimits', { level }) }}</span>
              <el-tag
                v-if="config.quota.defaultLevel === level"
                type="success"
                size="small"
              >
                {{ $t('admin.config.defaultLevel') }}
              </el-tag>
            </div>
          </template>
          <el-row :gutter="20">
            <el-col :span="6">
              <el-form-item :label="$t('admin.config.maxInstances')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['maxInstances']"
                  :min="1"
                  :max="1000"
                  :controls="false"
                  :step="1"
                  style="width: 100%"
                />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item :label="$t('admin.config.maxCPU')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['maxResources']['cpu']"
                  :min="1"
                  :max="10240"
                  :controls="false"
                  :step="1"
                  style="width: 100%"
                />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item :label="$t('admin.config.maxMemoryMB')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['maxResources']['memory']"
                  :min="128"
                  :max="10485760"
                  :controls="false"
                  :step="128"
                  style="width: 100%"
                />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item :label="$t('admin.config.maxDiskMB')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['maxResources']['disk']"
                  :min="512"
                  :max="1024000000"
                  :controls="false"
                  :step="512"
                  style="width: 100%"
                />
              </el-form-item>
            </el-col>
          </el-row>
          <el-row :gutter="20">
            <el-col :span="6">
              <el-form-item :label="$t('admin.config.maxBandwidthMbps')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['maxResources']['bandwidth']"
                  :min="1"
                  :max="1000000"
                  :controls="false"
                  :step="1"
                  style="width: 100%"
                />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item :label="$t('admin.config.trafficLimitMB')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['maxTraffic']"
                  :min="1024"
                  :max="1024000000"
                  :controls="false"
                  :step="1024"
                  style="width: 100%"
                />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item :label="$t('admin.config.expiryDays')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['expiryDays']"
                  :min="0"
                  :max="36500"
                  :controls="false"
                  :step="1"
                  style="width: 100%"
                />
                <div class="form-item-hint">
                  {{ $t('admin.config.expiryDaysHint') }}
                </div>
              </el-form-item>
            </el-col>
          </el-row>
        </el-card>
      </el-col>
    </el-row>
  </el-form>
</template>

<script setup>
defineProps({
  config: { type: Object, required: true },
  loading: { type: Boolean, default: false }
})
</script>

<style scoped>
.config-form {
  max-height: 600px;
  overflow-y: auto;
}
.level-card {
  border: 1px solid var(--border-color);
}
.level-card.default-level {
  border-color: var(--el-color-success);
}
.level-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.level-title {
  font-weight: 500;
}
.form-item-hint {
  font-size: 12px;
  color: var(--text-color-tertiary);
  margin-top: 4px;
  line-height: 1.4;
}
</style>

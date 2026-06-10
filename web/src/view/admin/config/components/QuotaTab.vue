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
          v-for="level in levelKeys"
          :key="level"
          :label="$t('admin.config.levelN', { level })"
          :value="level"
        />
      </el-select>
    </el-form-item>

    <el-divider content-position="left">
      <div class="divider-title">
        <span>{{ $t('admin.config.levelLimitsConfig') }}</span>
        <el-button
          type="primary"
          size="small"
          @click="addLevel"
        >
          {{ $t('admin.config.addLevel') }}
        </el-button>
      </div>
    </el-divider>

    <!-- 等级限制配置 -->
    <el-row :gutter="15">
      <el-col
        v-for="level in levelKeys"
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
              <div class="level-actions">
                <el-tag
                  v-if="config.quota.defaultLevel === level"
                  type="success"
                  size="small"
                >
                  {{ $t('admin.config.defaultLevel') }}
                </el-tag>
                <el-button
                  size="small"
                  type="danger"
                  text
                  :disabled="levelKeys.length <= 1 || config.quota.defaultLevel === level"
                  @click="removeLevel(level)"
                >
                  {{ $t('common.delete') }}
                </el-button>
              </div>
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
              <el-form-item :label="$t('admin.config.maxSnapshots')">
                <el-input-number
                  v-model="config.quota.levelLimits[level]['maxSnapshots']"
                  :min="0"
                  :max="1000"
                  :controls="false"
                  :step="1"
                  style="width: 100%"
                />
                <div class="form-item-hint">
                  {{ $t('admin.config.maxSnapshotsHint') }}
                </div>
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
import { computed } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { DEFAULT_QUOTA_LEVEL_LIMITS, buildDefaultLevelLimit, getSortedLevelKeys } from '@/utils/levels'

const { t } = useI18n()

const props = defineProps({
  config: { type: Object, required: true },
  loading: { type: Boolean, default: false }
})

const levelKeys = computed(() => getSortedLevelKeys(props.config.quota.levelLimits))

const addLevel = () => {
  const nextLevel = (levelKeys.value.at(-1) || 0) + 1
  const previousLevel = levelKeys.value.at(-1)
  props.config.quota.levelLimits[nextLevel] = buildDefaultLevelLimit(nextLevel, props.config.quota.levelLimits[previousLevel], DEFAULT_QUOTA_LEVEL_LIMITS)
  ElMessage.success(t('admin.config.levelAdded', { level: nextLevel }))
}

const removeLevel = (level) => {
  if (props.config.quota.defaultLevel === level || levelKeys.value.length <= 1) return
  delete props.config.quota.levelLimits[level]
  ElMessage.success(t('admin.config.levelRemoved', { level }))
}
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
.divider-title,
.level-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.divider-title {
  justify-content: space-between;
  width: 100%;
}
.form-item-hint {
  font-size: 12px;
  color: var(--text-color-tertiary);
  margin-top: 4px;
  line-height: 1.4;
}
</style>

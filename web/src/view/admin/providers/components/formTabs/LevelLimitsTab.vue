<template>
  <div class="level-limits-container">
    <div style="margin-bottom: 12px; display: flex; justify-content: space-between; align-items: center;">
      <el-text
        type="info"
        size="small"
      >
        {{ $t('admin.providers.levelLimitsTip') }}
      </el-text>
      <el-button
        type="primary"
        size="small"
        @click="emit('reset-defaults')"
      >
        {{ $t('admin.providers.resetToDefault') }}
      </el-button>
    </div>

    <!-- 等级配置循环 -->
    <div
      v-for="level in 5"
      :key="level"
      class="level-config-card"
    >
      <div class="level-header">
        <el-tag
          :type="getLevelTagType(level)"
          size="large"
        >
          {{ $t('admin.providers.level') }} {{ level }}
        </el-tag>
      </div>

      <el-form
        :model="modelValue.levelLimits[level]"
        label-width="120px"
        class="level-form"
      >
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item :label="$t('admin.providers.maxInstances')">
              <el-input-number
                v-model="modelValue.levelLimits[level].maxInstances"
                :min="0"
                :max="1000"
                :controls="false"
                style="width: 100%;"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item :label="$t('admin.providers.maxTrafficMB')">
              <el-input-number
                v-model="modelValue.levelLimits[level].maxTraffic"
                :min="0"
                :max="1048576000"
                :step="1024"
                :controls="false"
                style="width: 100%;"
              />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item :label="$t('admin.providers.maxCPU')">
              <el-input-number
                v-model="modelValue.levelLimits[level].maxResources.cpu"
                :min="1"
                :max="10240"
                :controls="false"
                style="width: 100%;"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item :label="$t('admin.providers.maxMemoryMB')">
              <el-input-number
                v-model="modelValue.levelLimits[level].maxResources.memory"
                :min="128"
                :max="10485760"
                :step="128"
                :controls="false"
                style="width: 100%;"
              />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item :label="$t('admin.providers.maxDiskMB')">
              <el-input-number
                v-model="modelValue.levelLimits[level].maxResources.disk"
                :min="1024"
                :max="1024000000"
                :step="1024"
                :controls="false"
                style="width: 100%;"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item :label="$t('admin.providers.maxBandwidthMbps')">
              <el-input-number
                v-model="modelValue.levelLimits[level].maxResources.bandwidth"
                :min="10"
                :max="1000000"
                :step="10"
                :controls="false"
                style="width: 100%;"
              />
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>
    </div>
  </div>
</template>

<script setup>
defineProps({
  modelValue: {
    type: Object,
    required: true
  }
})

const emit = defineEmits(['reset-defaults'])

// 获取等级标签类型
const getLevelTagType = (level) => {
  const types = {
    1: 'info',
    2: 'success',
    3: 'warning',
    4: 'danger',
    5: 'primary'
  }
  return types[level] || 'info'
}
</script>

<style scoped>
.level-limits-container {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

.level-config-card {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  padding: 15px;
  margin-bottom: 15px;
  background-color: #f9fafb;
  transition: all 0.3s;
}

.level-config-card:hover {
  border-color: #409eff;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
}

.level-header {
  margin-bottom: 15px;
  text-align: center;
}

.level-form {
  margin-top: 10px;
}
</style>

<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <el-form-item
      :label="$t('admin.providers.expiresAt')"
      prop="expiresAt"
    >
      <el-date-picker
        v-model="modelValue.expiresAt"
        type="datetime"
        :placeholder="$t('admin.providers.expiresAtPlaceholder')"
        format="YYYY-MM-DD HH:mm:ss"
        value-format="YYYY-MM-DD HH:mm:ss"
        :disabled-date="(time) => time.getTime() < Date.now() - 8.64e7"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.expiresAtTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 并发控制设置 -->
    <el-divider content-position="left">
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.concurrencyControl') }}</span>
    </el-divider>
    
    <el-form-item
      :label="$t('admin.providers.allowConcurrentTasks')"
      prop="allowConcurrentTasks"
    >
      <el-switch
        v-model="modelValue.allowConcurrentTasks"
        :active-text="$t('common.yes')"
        :inactive-text="$t('common.no')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.allowConcurrentTasksTip') }}
        </el-text>
      </div>
    </el-form-item>

    <el-form-item
      v-if="modelValue.allowConcurrentTasks"
      :label="$t('admin.providers.maxConcurrentTasks')"
      prop="maxConcurrentTasks"
    >
      <el-input-number
        v-model="modelValue.maxConcurrentTasks"
        :min="1"
        :max="10"
        :step="1"
        :controls="false"
        placeholder="1"
        style="width: 200px"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.maxConcurrentTasksTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 任务轮询设置 -->
    <el-divider content-position="left">
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.taskPollingSettings') }}</span>
    </el-divider>
    
    <el-form-item
      :label="$t('admin.providers.enableTaskPolling')"
      prop="enableTaskPolling"
    >
      <el-switch
        v-model="modelValue.enableTaskPolling"
        :active-text="$t('common.yes')"
        :inactive-text="$t('common.no')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.enableTaskPollingTip') }}
        </el-text>
      </div>
    </el-form-item>

    <el-form-item
      v-if="modelValue.enableTaskPolling"
      :label="$t('admin.providers.taskPollInterval')"
      prop="taskPollInterval"
    >
      <el-input-number
        v-model="modelValue.taskPollInterval"
        :min="5"
        :max="300"
        :step="5"
        :controls="false"
        placeholder="60"
        style="width: 200px"
      />
      <span style="margin-left: 10px; color: #666;">{{ $t('common.seconds') }}</span>
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.taskPollIntervalTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 操作执行规则设置 -->
    <el-divider content-position="left">
      <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.executionRules') }}</span>
    </el-divider>
    
    <el-form-item
      :label="$t('admin.providers.executionRule')"
      prop="executionRule"
    >
      <el-select
        v-model="modelValue.executionRule"
        :placeholder="$t('admin.providers.executionRulePlaceholder')"
        style="width: 200px"
      >
        <el-option
          :label="$t('admin.providers.executionRuleAuto')"
          value="auto"
        >
          <span>{{ $t('admin.providers.executionRuleAuto') }}</span>
          <span style="float: right; color: #8492a6; font-size: 12px;">{{ $t('admin.providers.executionRuleAutoTip') }}</span>
        </el-option>
        <el-option
          :label="$t('admin.providers.executionRuleAPIOnly')"
          value="api_only"
        >
          <span>{{ $t('admin.providers.executionRuleAPIOnly') }}</span>
          <span style="float: right; color: #8492a6; font-size: 12px;">{{ $t('admin.providers.executionRuleAPIOnlyTip') }}</span>
        </el-option>
        <el-option
          :label="$t('admin.providers.executionRuleSSHOnly')"
          value="ssh_only"
        >
          <span>{{ $t('admin.providers.executionRuleSSHOnly') }}</span>
          <span style="float: right; color: #8492a6; font-size: 12px;">{{ $t('admin.providers.executionRuleSSHOnlyTip') }}</span>
        </el-option>
      </el-select>
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.executionRuleTip') }}
        </el-text>
      </div>
    </el-form-item>
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

<template>
  <el-form
    v-loading="loading"
    :model="config"
    label-width="140px"
    class="config-form"
  >
    <el-alert
      :title="$t('admin.config.smtpConfigDesc')"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      {{ $t('admin.config.smtpConfigHint') }}
    </el-alert>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.smtpHost')">
          <el-input
            v-model="config.auth.emailSMTPHost"
            :placeholder="$t('admin.config.smtpHostPlaceholder')"
          />
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.smtpPort')">
          <el-input-number
            v-model="config.auth.emailSMTPPort"
            :min="1"
            :max="65535"
            :controls="false"
            :placeholder="$t('admin.config.smtpPortPlaceholder')"
            style="width: 100%"
          />
        </el-form-item>
      </el-col>
    </el-row>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.emailUsername')">
          <el-input
            v-model="config.auth.emailUsername"
            :placeholder="$t('admin.config.emailUsernamePlaceholder')"
          />
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.emailPassword')">
          <el-input
            v-model="config.auth.emailPassword"
            type="password"
            :placeholder="$t('admin.config.emailPasswordPlaceholder')"
            show-password
          />
        </el-form-item>
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
.config-form :deep(.el-form-item__label) {
  font-weight: 500;
  color: var(--text-color-secondary);
}
.config-form :deep(.el-alert) {
  margin-bottom: 20px;
}
</style>

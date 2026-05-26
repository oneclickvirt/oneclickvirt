<template>
  <el-form
    v-loading="loading"
    :model="config"
    label-width="140px"
    class="config-form"
  >
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.emailLogin')">
          <el-switch v-model="config.auth.enableEmail" />
          <div class="form-item-hint">
            {{ $t('admin.config.emailLoginHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item
          :label="$t('admin.config.publicRegistration')"
          :help="$t('admin.config.publicRegistrationHelp')"
        >
          <el-switch v-model="config.auth.enablePublicRegistration" />
        </el-form-item>
      </el-col>
    </el-row>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.telegramLogin')">
          <el-switch v-model="config.auth.enableTelegram" />
          <div class="form-item-hint">
            {{ $t('admin.config.telegramLoginHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.qqLogin')">
          <el-switch v-model="config.auth.enableQQ" />
          <div class="form-item-hint">
            {{ $t('admin.config.qqLoginHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item label="OAuth2">
          <el-switch v-model="config.auth.enableOAuth2" />
          <div class="form-item-hint">
            {{ $t('admin.config.oauth2Hint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.inviteCodeSystem')">
          <el-switch v-model="config.inviteCode.enabled" />
          <div class="form-item-hint">
            {{ $t('admin.config.inviteCodeSystemHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.kycFeature')">
          <el-switch v-model="config.auth.enableKYC" />
          <div class="form-item-hint">
            {{ $t('admin.config.kycFeatureHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.domainFeature')">
          <el-switch v-model="config.auth.enableDomain" />
          <div class="form-item-hint">
            {{ $t('admin.config.domainFeatureHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.checkinFeature')">
          <el-switch v-model="config.auth.enableCheckin" />
          <div class="form-item-hint">
            {{ $t('admin.config.checkinFeatureHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.captchaFeature')">
          <el-switch v-model="config.captcha.enabled" />
          <div class="form-item-hint">
            {{ $t('admin.config.captchaFeatureHint') }}
          </div>
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
.form-item-hint {
  font-size: 12px;
  color: var(--text-color-tertiary);
  margin-top: 4px;
  line-height: 1.4;
}
</style>

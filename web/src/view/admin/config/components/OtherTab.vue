<template>
  <el-form
    v-loading="loading"
    :model="config"
    label-width="140px"
    class="config-form"
  >
    <!-- Logo 设置 -->
    <el-divider content-position="left">
      {{ $t('admin.config.logoSettings') }}
    </el-divider>

    <el-row :gutter="20">
      <el-col :span="16">
        <el-form-item :label="$t('admin.config.customLogoURL')">
          <el-input
            v-model="config.other.logoURL"
            :placeholder="$t('admin.config.customLogoURLPlaceholder')"
            clearable
          />
          <div class="form-item-hint">
            {{ $t('admin.config.customLogoURLHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col
        v-if="config.other.logoURL"
        :span="8"
      >
        <el-form-item :label="$t('admin.config.logoPreview')">
          <img
            :src="config.other.logoURL"
            alt="Logo Preview"
            style="height:40px;max-width:160px;object-fit:contain;border:1px solid var(--border-color);border-radius:4px;padding:4px;background:var(--bg-color-secondary);"
            @error="$event.target.style.display='none'"
            @load="$event.target.style.display=''"
          >
        </el-form-item>
      </el-col>
    </el-row>

    <el-row :gutter="20">
      <el-col :span="16">
        <el-form-item :label="$t('admin.config.customSiteName')">
          <el-input
            v-model="config.other.siteName"
            :placeholder="$t('admin.config.customSiteNamePlaceholder')"
            clearable
          />
          <div class="form-item-hint">
            {{ $t('admin.config.customSiteNameHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <el-divider content-position="left">
      {{ $t('admin.config.languageSettings') }}
    </el-divider>

    <el-alert
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      <template #title>
        <strong>{{ $t('admin.config.languageForceNote') }}</strong>
      </template>
      {{ $t('admin.config.languageForceDesc') }}
    </el-alert>

    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.defaultLanguage')">
          <el-select
            v-model="config.other.defaultLanguage"
            :placeholder="$t('admin.config.selectDefaultLanguage')"
            style="width: 100%"
            clearable
          >
            <el-option
              value=""
              :label="$t('admin.config.browserLanguage')"
            />
            <el-option
              value="zh-CN"
              label="中文"
            />
            <el-option
              value="en-US"
              label="English"
            />
          </el-select>
          <div class="form-item-hint">
            {{ $t('admin.config.defaultLanguageHint') }}
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
.form-item-hint {
  font-size: 12px;
  color: var(--text-color-tertiary);
  margin-top: 4px;
  line-height: 1.4;
}
</style>

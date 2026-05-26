<template>
  <el-form
    v-loading="loading"
    :model="config"
    label-width="140px"
    class="config-form"
  >
    <!-- Telegram配置 -->
    <el-card
      class="oauth-card"
      shadow="never"
    >
      <template #header>
        <div class="oauth-header">
          <span>{{ $t('admin.config.telegramConfig') }}</span>
          <el-switch v-model="config.auth.enableTelegram" />
        </div>
      </template>
      <el-form-item :label="$t('admin.config.telegramBotToken')">
        <el-input
          v-model="config.auth.telegramBotToken"
          :placeholder="$t('admin.config.telegramBotTokenPlaceholder')"
          :disabled="!config.auth.enableTelegram"
        />
      </el-form-item>
    </el-card>

    <!-- QQ配置 -->
    <el-card
      class="oauth-card"
      shadow="never"
    >
      <template #header>
        <div class="oauth-header">
          <span>{{ $t('admin.config.qqConfig') }}</span>
          <el-switch v-model="config.auth.enableQQ" />
        </div>
      </template>
      <el-row :gutter="20">
        <el-col :span="12">
          <el-form-item :label="$t('admin.config.qqAppId')">
            <el-input
              v-model="config.auth.qqAppID"
              :placeholder="$t('admin.config.qqAppIdPlaceholder')"
              :disabled="!config.auth.enableQQ"
            />
          </el-form-item>
        </el-col>
        <el-col :span="12">
          <el-form-item :label="$t('admin.config.qqAppKey')">
            <el-input
              v-model="config.auth.qqAppKey"
              :placeholder="$t('admin.config.qqAppKeyPlaceholder')"
              :disabled="!config.auth.enableQQ"
            />
          </el-form-item>
        </el-col>
      </el-row>
    </el-card>
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
.oauth-card {
  margin-bottom: 20px;
}
.oauth-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
</style>

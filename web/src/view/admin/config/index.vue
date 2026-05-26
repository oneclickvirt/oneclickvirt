<template>
  <div class="config-container">
    <el-card>
      <template #header>
        <div class="config-header">
          <span>{{ $t('admin.config.title') }}</span>
        </div>
      </template>

      <!-- 配置分类标签页 -->
      <el-tabs
        v-model="activeTab"
        type="border-card"
        class="config-tabs"
      >
        <!-- 基础认证配置 -->
        <el-tab-pane
          :label="$t('admin.config.basicAuth')"
          name="auth"
        >
          <AuthTab
            :config="config"
            :loading="loading"
          />
        </el-tab-pane>

        <!-- 邮箱SMTP配置 -->
        <el-tab-pane
          :label="$t('admin.config.emailConfig')"
          name="email"
        >
          <EmailTab
            :config="config"
            :loading="loading"
          />
        </el-tab-pane>

        <!-- 第三方登录配置 -->
        <el-tab-pane
          :label="$t('admin.config.thirdPartyLogin')"
          name="oauth"
        >
          <OAuthTab
            :config="config"
            :loading="loading"
          />
        </el-tab-pane>

        <!-- 用户等级配置 -->
        <el-tab-pane
          :label="$t('admin.config.userLevel')"
          name="quota"
        >
          <QuotaTab
            :config="config"
            :loading="loading"
          />
        </el-tab-pane>

        <!-- 实例类型权限配置 -->
        <el-tab-pane
          :label="$t('admin.config.instancePermissions')"
          name="instancePermissions"
        >
          <InstancePermissionsTab
            :permissions="instanceTypePermissions"
            :loading="loading"
          />
        </el-tab-pane>

        <!-- 实名认证配置 -->
        <el-tab-pane
          :label="$t('admin.config.kycConfig')"
          name="kyc"
        >
          <KycTab
            :config="config"
            :loading="loading"
          />
        </el-tab-pane>

        <!-- 其他配置 -->
        <el-tab-pane
          :label="$t('admin.config.otherConfig')"
          name="other"
        >
          <OtherTab
            :config="config"
            :loading="loading"
          />
        </el-tab-pane>
      </el-tabs>

      <!-- 底部操作按钮 -->
      <div class="config-actions">
        <el-button
          type="primary"
          size="large"
          :loading="loading"
          @click="saveConfig"
        >
          {{ $t('admin.config.saveCurrentConfig') }}
        </el-button>
        <el-button 
          size="large"
          @click="resetConfig"
        >
          {{ $t('admin.config.resetConfig') }}
        </el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { useConfigManagement } from './composables/useConfigManagement'
import AuthTab from './components/AuthTab.vue'
import EmailTab from './components/EmailTab.vue'
import OAuthTab from './components/OAuthTab.vue'
import QuotaTab from './components/QuotaTab.vue'
import InstancePermissionsTab from './components/InstancePermissionsTab.vue'
import KycTab from './components/KycTab.vue'
import OtherTab from './components/OtherTab.vue'

const {
  activeTab, config, instanceTypePermissions, loading,
  saveConfig, resetConfig, t
} = useConfigManagement()
</script>

<style scoped>
.config-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: var(--text-color-primary);
  }
}

.config-tabs {
  margin-bottom: 20px;
}

.config-tabs :deep(.el-tabs__content) {
  padding: 20px;
}

.config-form {
  max-height: 600px;
  overflow-y: auto;
}

.oauth-card {
  margin-bottom: 16px;
}

.oauth-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.level-card {
  border: 2px solid #f0f0f0;
  transition: all 0.3s ease;
}

.level-card:hover {
  border-color: #16a34a;
  box-shadow: 0 2px 12px 0 rgba(22, 163, 74, 0.15);
}

.level-card.default-level {
  border-color: #16a34a;
  background-color: var(--success-bg);
}

.level-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.level-title {
  font-weight: 600;
  color: var(--text-color-primary);
}

.config-actions {
  display: flex;
  justify-content: center;
  gap: 16px;
  padding: 20px 0;
  border-top: 1px solid #f0f0f0;
  margin-top: 20px;
}

/* 响应式设计 */
@media (max-width: 768px) {
  .config-container {
    padding: 10px;
  }
  
  .config-form {
    max-height: none;
  }
  
  .level-card :deep(.el-col) {
    margin-bottom: 10px;
  }
  
  .config-actions {
    flex-direction: column;
    align-items: center;
  }
  
  .config-actions .el-button {
    width: 100%;
    max-width: 200px;
  }
}

/* 标签页样式 */
.config-tabs :deep(.el-tabs__header) {
  margin-bottom: 0;
}

.config-tabs :deep(.el-tabs__nav-wrap) {
  padding: 0 10px;
}

.config-tabs :deep(.el-tabs__item) {
  padding: 0 20px;
  font-weight: 500;
}

/* 表单样式 */
.config-form :deep(.el-form-item__label) {
  font-weight: 500;
  color: var(--text-color-secondary);
}

.config-form :deep(.el-alert) {
  margin-bottom: 20px;
}

.form-item-hint {
  font-size: 12px;
  color: var(--text-color-tertiary);
  margin-top: 4px;
  line-height: 1.4;
}
</style>
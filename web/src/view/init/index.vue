<template>
  <div class="init-container">
    <div class="init-bg-pattern" />
    <div class="init-card">
      <!-- Header -->
      <div class="init-header">
        <div class="init-logo">
          <svg
            viewBox="0 0 24 24"
            width="40"
            height="40"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M12 2L2 7l10 5 10-5-10-5z" />
            <path d="M2 17l10 5 10-5" />
            <path d="M2 12l10 5 10-5" />
          </svg>
        </div>
        <h1>{{ t('init.title') }}</h1>
        <p>{{ t('init.subtitle') }}</p>
      </div>

      <!-- Progress Panel (shown after submit) -->
      <InitProgressPanel
        v-if="showProgress"
        :status="progressStatus"
        :steps="progressSteps"
        :percent="progressPercent"
        :bar-status="progressBarStatus"
        :tag-type="progressTagType"
        :status-text="progressStatusText"
        @retry="retryInit"
        @go-home="goHome"
      />

      <!-- Form Panel (hidden after submit) -->
      <template v-if="!showProgress">
        <!-- Steps indicator -->
        <el-steps
          :active="stepIndex"
          finish-status="success"
          align-center
          class="init-steps"
        >
          <el-step :title="t('init.database.tabLabel')" />
          <el-step :title="t('init.admin.tabLabel')" />
          <el-step :title="t('init.user.tabLabel')" />
        </el-steps>

        <!-- Step 1: Database -->
        <div
          v-show="activeTab === 'database'"
          class="step-content"
        >
          <el-form 
            ref="databaseFormRef" 
            :model="databaseForm" 
            :rules="databaseRules" 
            label-width="140px" 
            label-position="top"
            size="large"
          >
            <el-form-item
              :label="t('init.database.type')"
              prop="type"
            >
              <el-radio-group
                v-model="databaseForm.type"
                @change="onDatabaseTypeChange"
              >
                <el-radio-button label="mysql">
                  MySQL
                </el-radio-button>
                <el-radio-button label="mariadb">
                  MariaDB
                </el-radio-button>
              </el-radio-group>
              <div class="field-hint">
                <el-text
                  v-if="dbRecommendation"
                  size="small"
                  type="success"
                >
                  {{ dbRecommendation.reason }} ({{ t('init.database.architecture') }}: {{ dbRecommendation.architecture }})
                </el-text>
                <el-text
                  v-else
                  size="small"
                  type="info"
                >
                  {{ t('init.database.autoSelectHint') }}
                </el-text>
              </div>
            </el-form-item>

            <el-row :gutter="16">
              <el-col :span="16">
                <el-form-item
                  :label="t('init.database.host')"
                  prop="host"
                >
                  <el-input
                    v-model="databaseForm.host"
                    placeholder="127.0.0.1"
                  />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item
                  :label="t('init.database.port')"
                  prop="port"
                >
                  <el-input
                    v-model="databaseForm.port"
                    placeholder="3306"
                  />
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item
              :label="t('init.database.dbName')"
              prop="database"
            >
              <el-input
                v-model="databaseForm.database"
                placeholder="oneclickvirt"
              />
            </el-form-item>

            <el-row :gutter="16">
              <el-col :span="12">
                <el-form-item
                  :label="t('init.database.username')"
                  prop="username"
                >
                  <el-input
                    v-model="databaseForm.username"
                    placeholder="root"
                  />
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  :label="t('init.database.password')"
                  prop="password"
                >
                  <el-input
                    v-model="databaseForm.password"
                    type="password"
                    :placeholder="t('init.database.passwordPlaceholder')"
                    show-password
                  />
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item>
              <el-button
                type="info"
                plain
                :loading="testingConnection"
                @click="testDatabaseConnection"
              >
                {{ t('init.database.testConnection') }}
              </el-button>
              <span
                v-if="connectionTestResult"
                :class="connectionTestResult.success ? 'test-success' : 'test-error'"
              >
                {{ connectionTestResult.message }}
              </span>
            </el-form-item>
          </el-form>
        </div>

        <!-- Step 2: Admin -->
        <div
          v-show="activeTab === 'admin'"
          class="step-content"
        >
          <el-form
            ref="adminFormRef"
            :model="initForm.admin"
            :rules="adminRules"
            label-position="top"
            size="large"
          >
            <el-form-item
              :label="t('init.admin.username')"
              prop="username"
            >
              <el-input
                v-model="initForm.admin.username"
                :placeholder="t('init.admin.usernamePlaceholder')"
                clearable
                prefix-icon="User"
              />
            </el-form-item>
            <el-form-item
              :label="t('init.admin.password')"
              prop="password"
            >
              <el-input
                v-model="initForm.admin.password"
                type="password"
                :placeholder="t('init.admin.passwordPlaceholder')"
                show-password
                clearable
                prefix-icon="Lock"
              />
              <div class="field-hint">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ t('init.admin.passwordHint') }}
                </el-text>
              </div>
            </el-form-item>
            <el-form-item
              :label="t('init.admin.confirmPassword')"
              prop="confirmPassword"
            >
              <el-input
                v-model="initForm.admin.confirmPassword"
                type="password"
                :placeholder="t('init.admin.confirmPasswordPlaceholder')"
                show-password
                clearable
                prefix-icon="Lock"
              />
            </el-form-item>
            <el-form-item
              :label="t('init.admin.email')"
              prop="email"
            >
              <el-input
                v-model="initForm.admin.email"
                :placeholder="t('init.admin.emailPlaceholder')"
                clearable
                prefix-icon="Message"
              />
            </el-form-item>
          </el-form>
        </div>

        <!-- Step 3: User -->
        <div
          v-show="activeTab === 'user'"
          class="step-content"
        >
          <el-form
            ref="userFormRef"
            :model="initForm.user"
            :rules="userRules"
            label-position="top"
            size="large"
          >
            <el-form-item :label="t('init.user.enableStatus')">
              <div class="enable-toggle">
                <el-switch
                  v-model="initForm.user.enabled"
                  :active-text="t('common.enabled')"
                  :inactive-text="t('common.disabled')"
                />
                <el-text
                  size="small"
                  type="warning"
                  class="enable-hint"
                >
                  {{ t('init.user.enableHint') }}
                </el-text>
              </div>
            </el-form-item>
            <template v-if="initForm.user.enabled">
              <el-form-item
                :label="t('init.user.username')"
                prop="username"
              >
                <el-input
                  v-model="initForm.user.username"
                  :placeholder="t('init.user.usernamePlaceholder')"
                  clearable
                  prefix-icon="User"
                />
              </el-form-item>
              <el-form-item
                :label="t('init.user.password')"
                prop="password"
              >
                <el-input
                  v-model="initForm.user.password"
                  type="password"
                  :placeholder="t('init.user.passwordPlaceholder')"
                  show-password
                  clearable
                  prefix-icon="Lock"
                />
                <div class="field-hint">
                  <el-text
                    size="small"
                    type="info"
                  >
                    {{ t('init.user.passwordHint') }}
                  </el-text>
                </div>
              </el-form-item>
              <el-form-item
                :label="t('init.user.confirmPassword')"
                prop="confirmPassword"
              >
                <el-input
                  v-model="initForm.user.confirmPassword"
                  type="password"
                  :placeholder="t('init.user.confirmPasswordPlaceholder')"
                  show-password
                  clearable
                  prefix-icon="Lock"
                />
              </el-form-item>
              <el-form-item
                :label="t('init.user.email')"
                prop="email"
              >
                <el-input
                  v-model="initForm.user.email"
                  :placeholder="t('init.user.emailPlaceholder')"
                  clearable
                  prefix-icon="Message"
                />
              </el-form-item>
            </template>
          </el-form>
        </div>

        <!-- Navigation buttons -->
        <div class="init-actions">
          <el-button
            v-if="activeTab !== 'database'"
            size="large"
            @click="prevStep"
          >
            {{ t('common.back') }}
          </el-button>
          <el-button
            type="info"
            plain
            size="large"
            @click="fillDefaultData"
          >
            {{ t('init.fillDefaults') }}
          </el-button>
          <div style="flex: 1" />
          <el-button
            v-if="activeTab !== 'user'"
            type="primary"
            size="large"
            @click="nextStep"
          >
            {{ t('init.nextStep') }}
          </el-button>
          <el-button
            v-else
            type="primary"
            :loading="loading"
            :disabled="loading || !isFormValid"
            size="large"
            @click="handleInit"
          >
            {{ t('init.initSystem') }}
          </el-button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import InitProgressPanel from './components/InitProgressPanel.vue'
import useInit from './useInit'
import { useI18n } from 'vue-i18n'
const { t } = useI18n()

const {
  adminFormRef, userFormRef, databaseFormRef,
  loading, testingConnection, connectionTestResult,
  activeTab, dbRecommendation,
  showProgress, progressStatus, progressSteps,
  progressPercent, progressBarStatus, progressTagType, progressStatusText,
  stepIndex,
  databaseForm, initForm,
  adminRules, userRules, databaseRules,
  isFormValid,
  nextStep, prevStep,
  onDatabaseTypeChange, fillDefaultData,
  testDatabaseConnection, handleInit,
  retryInit, goHome
} = useInit()
</script>

<style scoped>
.init-container {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #f0fdf4 0%, #ecfdf5 50%, #f0f9ff 100%);
  padding: 24px;
  position: relative;
  overflow: hidden;
}

.init-bg-pattern {
  position: absolute;
  inset: 0;
  background-image:
    radial-gradient(circle at 20% 30%, rgba(34, 197, 94, 0.08) 0%, transparent 50%),
    radial-gradient(circle at 80% 70%, rgba(59, 130, 246, 0.06) 0%, transparent 50%);
  z-index: 0;
}

.init-card {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(20px);
  padding: 48px 40px 36px;
  border-radius: 20px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.06), 0 1px 2px rgba(0, 0, 0, 0.04);
  width: 100%;
  max-width: 620px;
  border: 1px solid rgba(34, 197, 94, 0.12);
  position: relative;
  z-index: 1;
}

.init-header {
  text-align: center;
  margin-bottom: 32px;
}

.init-logo {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  border-radius: 16px;
  background: linear-gradient(135deg, #16a34a, #22c55e);
  color: white;
  margin-bottom: 16px;
  box-shadow: 0 4px 12px rgba(34, 197, 94, 0.3);
}

.init-header h1 {
  font-size: 28px;
  font-weight: 700;
  color: #111827;
  margin: 0 0 8px;
}

.init-header p {
  font-size: 15px;
  color: #6b7280;
  margin: 0;
}

.init-steps {
  margin-bottom: 32px;
}

.step-content {
  min-height: 280px;
}

.init-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 24px;
  padding-top: 24px;
  border-top: 1px solid #f3f4f6;
}

.field-hint {
  margin-top: 6px;
}

.enable-toggle {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.enable-hint {
  line-height: 1.4;
}

.test-success {
  color: #16a34a;
  margin-left: 12px;
  font-size: 14px;
}

.test-error {
  color: #ef4444;
  margin-left: 12px;
  font-size: 14px;
}

:deep(.el-steps) {
  padding: 0 20px;
}

:deep(.el-step__title.is-finish) {
  color: #16a34a;
}

:deep(.el-step__head.is-finish) {
  color: #16a34a;
  border-color: #16a34a;
}

:deep(.el-step__head.is-process) {
  color: #16a34a;
  border-color: #16a34a;
}

:deep(.el-step__title.is-process) {
  color: #16a34a;
  font-weight: 600;
}

:deep(.el-form-item__label) {
  font-weight: 500;
  color: #374151;
}

:deep(.el-input__wrapper) {
  border-radius: 10px;
  box-shadow: 0 0 0 1px #e5e7eb inset;
  transition: all 0.2s;
}

:deep(.el-input__wrapper:hover) {
  box-shadow: 0 0 0 1px rgba(34, 197, 94, 0.4) inset;
}

:deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 2px rgba(34, 197, 94, 0.25) inset;
}

:deep(.el-button--primary) {
  background: #16a34a;
  border-color: #16a34a;
  border-radius: 10px;
}

:deep(.el-button--primary:hover) {
  background: #15803d;
  border-color: #15803d;
}

:deep(.el-radio-button__inner) {
  border-radius: 8px;
}

@media (max-width: 640px) {
  .init-card {
    padding: 32px 20px 24px;
    border-radius: 16px;
  }

  .init-header h1 {
    font-size: 24px;
  }

  .init-actions {
    flex-wrap: wrap;
  }
}
</style>
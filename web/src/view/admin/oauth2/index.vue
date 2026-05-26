<template>
  <div class="oauth2-providers-container">
    <!-- OAuth2 功能未启用提示 -->
    <el-alert
      v-if="!oauth2Enabled"
      :title="$t('admin.oauth2.notEnabled')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      <template #default>
        <div>
          {{ $t('admin.oauth2.notEnabledHint') }}
          <br>
          {{ $t('admin.oauth2.enableHint') }}
          <el-link
            type="primary"
            :underline="false"
            @click="goToConfig"
          >
            <strong>{{ $t('admin.oauth2.systemConfig') }}</strong>
          </el-link>
          {{ $t('admin.oauth2.enableHint2') }}
        </div>
      </template>
    </el-alert>

    <el-card
      shadow="never"
      class="providers-card"
    >
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.oauth2.title') }}</span>
          <el-button
            type="primary"
            @click="handleAdd"
          >
            {{ $t('admin.oauth2.addProvider') }}
          </el-button>
        </div>
      </template>

      <el-table
        v-loading="loading"
        :data="providers"
        class="providers-table"
        :row-style="{ height: '60px' }"
        :cell-style="{ padding: '12px 0' }"
        :header-cell-style="{ background: '#f5f7fa', padding: '14px 0', fontWeight: '600' }"
      >
        <el-table-column
          prop="id"
          label="ID"
          width="80"
          align="center"
        />
        <el-table-column
          prop="displayName"
          :label="$t('admin.oauth2.displayName')"
          min-width="140"
        />
        <el-table-column
          prop="name"
          :label="$t('admin.oauth2.identifierName')"
          min-width="140"
        />
        <el-table-column
          :label="$t('common.status')"
          width="100"
          align="center"
        >
          <template #default="{ row }">
            <el-tag
              :type="row.enabled ? 'success' : 'info'"
              size="default"
            >
              {{ row.enabled ? $t('common.enabled') : $t('common.disabled') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.oauth2.registrationStats')"
          width="140"
          align="center"
        >
          <template #default="{ row }">
            <span v-if="row.maxRegistrations > 0">
              {{ row.currentRegistrations }} / {{ row.maxRegistrations }}
            </span>
            <span v-else>
              {{ row.totalUsers }} ({{ $t('admin.oauth2.unlimited') }})
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="clientId"
          :label="$t('admin.oauth2.clientIdLabel')"
          min-width="220"
          show-overflow-tooltip
        />
        <el-table-column
          prop="redirectUrl"
          :label="$t('admin.oauth2.callbackUrl')"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column
          :label="$t('common.actions')"
          width="300"
          fixed="right"
          align="center"
        >
          <template #default="{ row }">
            <div class="action-buttons">
              <el-button
                size="small"
                @click="handleEdit(row)"
              >
                {{ $t('common.edit') }}
              </el-button>
              <el-button
                size="small"
                type="warning"
                @click="handleResetCount(row)"
              >
                {{ $t('admin.oauth2.resetCount') }}
              </el-button>
              <el-button
                size="small"
                type="danger"
                @click="handleDelete(row)"
              >
                {{ $t('common.delete') }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 添加/编辑对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="900px"
      :close-on-click-modal="false"
    >
      <template #header>
        <div class="dialog-header">
          <span>{{ dialogTitle }}</span>
          <div
            v-if="!isEdit"
            class="preset-selector"
          >
            <el-select
              v-model="selectedPreset"
              :placeholder="$t('admin.oauth2.selectPreset')"
              size="small"
              style="width: 200px"
              clearable
              @change="handlePresetChange"
            >
              <el-option
                label="Linux.do"
                value="linuxdo"
              />
              <el-option
                label="IDCFlare"
                value="idcflare"
              />
              <el-option
                label="GitHub"
                value="github"
              />
              <el-option
                label="GitLab"
                value="gitlab"
              />
              <el-option
                label="Gitea"
                value="gitea"
              />
              <el-option
                label="Google"
                value="google"
              />
              <el-option
                label="Microsoft"
                value="microsoft"
              />
              <el-option
                label="Discord"
                value="discord"
              />
              <el-option
                :label="$t('admin.oauth2.genericOAuth2')"
                value="generic"
              />
              <el-option
                :label="$t('admin.oauth2.customOAuth2')"
                value="custom"
              />
            </el-select>
          </div>
        </div>
      </template>
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="120px"
        class="oauth2-form"
      >
        <el-tabs
          v-model="activeTab"
          class="oauth2-tabs"
        >
          <el-tab-pane
            :label="$t('admin.oauth2.basicConfig')"
            name="basic"
          >
            <div class="form-section">
              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item
                    :label="$t('admin.oauth2.displayName')"
                    prop="displayName"
                  >
                    <el-input
                      v-model="formData.displayName"
                      :placeholder="$t('admin.oauth2.displayNamePlaceholder')"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item
                    :label="$t('admin.oauth2.identifierName')"
                    prop="name"
                  >
                    <el-input
                      v-model="formData.name"
                      :placeholder="$t('admin.oauth2.identifierNamePlaceholder')"
                      :disabled="isEdit"
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item :label="$t('admin.oauth2.enableStatus')">
                    <el-switch
                      v-model="formData.enabled"
                      :active-text="$t('common.enable')"
                      :inactive-text="$t('common.disable')"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item
                    :label="$t('admin.oauth2.displayOrder')"
                    prop="sort"
                  >
                    <el-input-number
                      v-model="formData.sort"
                      :min="0"
                      :max="999"
                      :controls="false"
                      style="width: 100%"
                    />
                    <span class="form-tip">{{ $t('admin.oauth2.displayOrderHint') }}</span>
                  </el-form-item>
                </el-col>
              </el-row>

              <el-divider content-position="left">
                {{ $t('admin.oauth2.oauth2Credentials') }}
              </el-divider>

              <el-form-item
                :label="$t('admin.oauth2.clientIdLabel')"
                prop="clientId"
              >
                <el-input
                  v-model="formData.clientId"
                  :placeholder="$t('admin.oauth2.clientIdPlaceholder')"
                />
              </el-form-item>

              <el-form-item
                :label="$t('admin.oauth2.clientSecretLabel')"
                prop="clientSecret"
              >
                <el-input
                  v-model="formData.clientSecret"
                  type="password"
                  :placeholder="isEdit ? $t('admin.oauth2.secretPlaceholderEdit') : $t('admin.oauth2.clientSecretPlaceholder')"
                  show-password
                />
              </el-form-item>
            </div>
          </el-tab-pane>

          <el-tab-pane
            :label="$t('admin.oauth2.oauth2Endpoints')"
            name="endpoints"
          >
            <el-form-item
              :label="$t('admin.oauth2.callbackUrl')"
              prop="redirectUrl"
            >
              <el-input
                v-model="formData.redirectUrl"
                placeholder="http://localhost:8888/api/v1/auth/oauth2/callback"
              />
            </el-form-item>

            <el-form-item
              :label="$t('admin.oauth2.authUrl')"
              prop="authUrl"
            >
              <el-input
                v-model="formData.authUrl"
                placeholder="https://provider.com/oauth2/authorize"
              />
            </el-form-item>

            <el-form-item
              :label="$t('admin.oauth2.tokenUrl')"
              prop="tokenUrl"
            >
              <el-input
                v-model="formData.tokenUrl"
                placeholder="https://provider.com/oauth2/token"
              />
            </el-form-item>

            <el-form-item
              :label="$t('admin.oauth2.userInfoUrl')"
              prop="userInfoUrl"
            >
              <el-input
                v-model="formData.userInfoUrl"
                placeholder="https://provider.com/api/user"
              />
            </el-form-item>
          </el-tab-pane>

          <el-tab-pane
            :label="$t('admin.oauth2.fieldMapping')"
            name="fields"
          >
            <el-alert
              type="info"
              :closable="false"
              style="margin-bottom: 20px"
            >
              <p>{{ $t('admin.oauth2.fieldMappingDesc') }}</p>
              <p>• {{ $t('admin.oauth2.requiredFields') }}</p>
              <p>• {{ $t('admin.oauth2.optionalFields') }}</p>
              <p>• {{ $t('admin.oauth2.nestedFieldsSupport') }}</p>
              <p>• {{ $t('admin.oauth2.defaultValuesInfo') }}</p>
            </el-alert>

            <el-form-item
              :label="$t('admin.oauth2.userIdField')"
              prop="userIdField"
            >
              <el-input
                v-model="formData.userIdField"
                :placeholder="$t('admin.oauth2.userIdFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item
              :label="$t('admin.oauth2.usernameField')"
              prop="usernameField"
            >
              <el-input
                v-model="formData.usernameField"
                :placeholder="$t('admin.oauth2.usernameFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.emailField')">
              <el-input
                v-model="formData.emailField"
                :placeholder="$t('admin.oauth2.emailFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.avatarField')">
              <el-input
                v-model="formData.avatarField"
                :placeholder="$t('admin.oauth2.avatarFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.nicknameField')">
              <el-input
                v-model="formData.nicknameField"
                :placeholder="$t('admin.oauth2.nicknameFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.trustLevelField')">
              <el-input
                v-model="formData.trustLevelField"
                :placeholder="$t('admin.oauth2.trustLevelFieldPlaceholder')"
              />
              <span class="form-tip">{{ $t('admin.oauth2.trustLevelFieldHint') }}</span>
            </el-form-item>
          </el-tab-pane>

          <el-tab-pane
            :label="$t('admin.oauth2.levelAndLimits')"
            name="level"
          >
            <el-form-item
              :label="$t('admin.oauth2.defaultUserLevel')"
              prop="defaultLevel"
            >
              <el-input-number
                v-model="formData.defaultLevel"
                :min="1"
                :max="10"
                :controls="false"
              />
              <span class="form-tip">{{ $t('admin.oauth2.defaultUserLevelHint') }}</span>
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.levelMappingConfig')">
              <div class="level-mapping">
                <div
                  v-for="(level, key) in formData.levelMapping"
                  :key="key"
                  class="mapping-item"
                >
                  <span>{{ $t('admin.oauth2.externalLevel') }} {{ key }} →</span>
                  <el-input-number
                    v-model="formData.levelMapping[key]"
                    :min="1"
                    :max="10"
                    :controls="false"
                    size="small"
                  />
                  <el-button
                    size="small"
                    type="danger"
                    text
                    @click="removeLevelMapping(key)"
                  >
                    {{ $t('common.delete') }}
                  </el-button>
                </div>
                <el-button
                  size="small"
                  @click="addLevelMapping"
                >
                  <el-icon><Plus /></el-icon>
                  {{ $t('admin.oauth2.addMapping') }}
                </el-button>
              </div>
              <span class="form-tip">{{ $t('admin.oauth2.levelMappingHint') }}</span>
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.registrationLimit')">
              <el-input-number
                v-model="formData.maxRegistrations"
                :min="0"
                :max="999999"
                :controls="false"
              />
              <span class="form-tip">{{ $t('admin.oauth2.registrationLimitHint') }}</span>
            </el-form-item>

            <el-form-item
              v-if="isEdit"
              :label="$t('admin.oauth2.currentRegistrations')"
            >
              <el-input-number
                v-model="formData.currentRegistrations"
                :controls="false"
                disabled
              />
            </el-form-item>
          </el-tab-pane>
        </el-tabs>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="handleSubmit"
        >
          {{ $t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 添加等级映射对话框 -->
    <el-dialog
      v-model="mappingDialogVisible"
      :title="$t('admin.oauth2.addLevelMapping')"
      width="400px"
    >
      <el-form label-width="120px">
        <el-form-item :label="$t('admin.oauth2.externalLevelValue')">
          <el-input
            v-model="newMapping.externalLevel"
            :placeholder="$t('admin.oauth2.externalLevelPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="$t('admin.oauth2.systemUserLevel')">
          <el-input-number
            v-model="newMapping.systemLevel"
            :min="1"
            :max="10"
            :controls="false"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="mappingDialogVisible = false">
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          @click="confirmAddMapping"
        >
          {{ $t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { Plus, Connection, Setting } from '@element-plus/icons-vue'
import useOAuth2 from './useOAuth2'

const {
  loading, providers,
  dialogVisible, dialogTitle, isEdit, submitting, activeTab, formRef, oauth2Enabled,
  mappingDialogVisible, selectedPreset, newMapping,
  formData, formRules,
  goToConfig, loadProviders,
  handlePresetChange, handleAdd, handleEdit, handleSubmit,
  handleDelete, handleResetCount,
  addLevelMapping, confirmAddMapping, removeLevelMapping
} = useOAuth2()
</script>

<style scoped lang="scss">
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: var(--text-color-primary);
  }
}

.providers-table {
  width: 100%;
  
  .action-buttons {
    display: flex;
    gap: 10px;
    justify-content: center;
    flex-wrap: wrap;
    padding: 4px 0;
    
    .el-button {
      margin: 0 !important;
    }
  }
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  
  .preset-selector {
    display: flex;
    align-items: center;
    gap: 8px;
  }
}

.oauth2-form {
  .oauth2-tabs {
    :deep(.el-tabs__content) {
      padding-top: 20px;
    }
  }

  .form-section {
    padding: 10px 0;
  }

  :deep(.el-form-item) {
    margin-bottom: 24px;
  }

  :deep(.el-divider) {
    margin: 30px 0 24px 0;
  }

  :deep(.el-input-number) {
    width: 100%;
  }
  
  :deep(.el-col) {
    .el-form-item {
      margin-right: 0;
    }
  }
}

.form-tip {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}

.level-mapping {
  .mapping-item {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;

    span {
      min-width: 120px;
    }
  }
}
</style>

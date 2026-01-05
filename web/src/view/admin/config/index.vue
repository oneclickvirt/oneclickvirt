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
          </el-form>
        </el-tab-pane>

        <!-- 邮箱SMTP配置 -->
        <el-tab-pane
          :label="$t('admin.config.emailConfig')"
          name="email"
        >
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
        </el-tab-pane>

        <!-- 第三方登录配置 -->
        <el-tab-pane
          :label="$t('admin.config.thirdPartyLogin')"
          name="oauth"
        >
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
              <el-form-item label="Bot Token">
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
                  <el-form-item label="App ID">
                    <el-input
                      v-model="config.auth.qqAppID"
                      :placeholder="$t('admin.config.qqAppIdPlaceholder')"
                      :disabled="!config.auth.enableQQ"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="App Key">
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
        </el-tab-pane>

        <!-- 用户等级配置 -->
        <el-tab-pane
          :label="$t('admin.config.userLevel')"
          name="quota"
        >
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
                  v-for="level in 5"
                  :key="level"
                  :label="$t('admin.config.levelN', { level })"
                  :value="level"
                />
              </el-select>
            </el-form-item>

            <el-divider content-position="left">
              {{ $t('admin.config.levelLimitsConfig') }}
            </el-divider>
            
            <!-- 等级限制配置 -->
            <el-row :gutter="15">
              <el-col
                v-for="level in 5"
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
                      <el-tag
                        v-if="config.quota.defaultLevel === level"
                        type="success"
                        size="small"
                      >
                        {{ $t('admin.config.defaultLevel') }}
                      </el-tag>
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
        </el-tab-pane>

        <!-- 实例类型权限配置 -->
        <el-tab-pane
          :label="$t('admin.config.instancePermissions')"
          name="instancePermissions"
        >
          <el-form
            v-loading="loading"
            :model="instanceTypePermissions"
            label-width="180px"
            class="config-form"
          >
            <el-alert
              :title="$t('admin.config.instancePermissionsDesc')"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              {{ $t('admin.config.instancePermissionsHint') }}
            </el-alert>
            
            <!-- 创建权限 -->
            <el-divider content-position="left">
              <el-icon><Plus /></el-icon> {{ $t('admin.config.createPermissions') }}
            </el-divider>
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.containerCreateMinLevel')">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForContainer"
                    :placeholder="$t('admin.config.selectLevel')"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="$t('admin.config.levelN', { level })"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    {{ $t('admin.config.containerCreateHint') }}
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.vmCreateMinLevel')">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForVM"
                    :placeholder="$t('admin.config.selectLevel')"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="$t('admin.config.levelN', { level })"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    {{ $t('admin.config.vmCreateHint') }}
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <!-- 删除权限 -->
            <el-divider content-position="left">
              <el-icon><Delete /></el-icon> {{ $t('admin.config.deletePermissions') }}
            </el-divider>
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.containerDeleteMinLevel')">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForDeleteContainer"
                    :placeholder="$t('admin.config.selectLevel')"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="$t('admin.config.levelN', { level })"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    {{ $t('admin.config.containerDeleteHint') }}
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.vmDeleteMinLevel')">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForDeleteVM"
                    :placeholder="$t('admin.config.selectLevel')"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="$t('admin.config.levelN', { level })"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    {{ $t('admin.config.vmDeleteHint') }}
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <!-- 重置系统权限 -->
            <el-divider content-position="left">
              <el-icon><Refresh /></el-icon> {{ $t('admin.config.resetPermissions') }}
            </el-divider>
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.containerResetMinLevel')">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForResetContainer"
                    :placeholder="$t('admin.config.selectLevel')"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="$t('admin.config.levelN', { level })"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    {{ $t('admin.config.containerResetHint') }}
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.vmResetMinLevel')">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForResetVM"
                    :placeholder="$t('admin.config.selectLevel')"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="$t('admin.config.levelN', { level })"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    {{ $t('admin.config.vmResetHint') }}
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-alert
              :title="$t('admin.config.permissionsSuggestions')"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 20px;"
            >
              <ul style="margin: 0; padding-left: 20px;">
                <li>{{ $t('admin.config.containerCreateSuggestion') }}</li>
                <li>{{ $t('admin.config.vmCreateSuggestion') }}</li>
                <li>{{ $t('admin.config.containerDeleteResetSuggestion') }}</li>
                <li>{{ $t('admin.config.vmDeleteResetSuggestion') }}</li>
              </ul>
            </el-alert>
          </el-form>
        </el-tab-pane>

        <!-- 其他配置 -->
        <el-tab-pane
          :label="$t('admin.config.otherConfig')"
          name="other"
        >
          <el-form
            v-loading="loading"
            :model="config"
            label-width="140px"
            class="config-form"
          >
            <el-alert
              :title="$t('admin.config.avatarUploadConfig')"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              {{ $t('admin.config.avatarUploadDesc') }}
            </el-alert>

            <el-divider content-position="left">
              {{ $t('admin.config.avatarUploadSettings') }}
            </el-divider>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.maxAvatarSize')">
                  <el-input-number
                    v-model="config.other.maxAvatarSize"
                    :min="0.5"
                    :max="10"
                    :step="0.5"
                    :precision="1"
                    :controls="false"
                    style="width: 100%"
                  />
                  <div class="form-item-hint">
                    {{ $t('admin.config.maxAvatarSizeHint') }}
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.supportedFormats')">
                  <el-tag
                    type="info"
                    style="margin-right: 8px;"
                  >
                    PNG
                  </el-tag>
                  <el-tag type="info">
                    JPEG
                  </el-tag>
                  <div class="form-item-hint">
                    {{ $t('admin.config.supportedFormatsHint') }}
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
                <strong>{{ $t('admin.config.languageForceNote') || '强制语言设置说明' }}</strong>
              </template>
              {{ $t('admin.config.languageForceDesc') || '当设置了系统默认语言（选择中文或English）后，所有用户将被强制使用该语言，用户的手动语言切换将被覆盖。留空时将根据用户浏览器语言自动选择。' }}
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
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox, ElNotification } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getAdminConfig, updateAdminConfig } from '@/api/config'
import { getInstanceTypePermissions, updateInstanceTypePermissions } from '@/api/admin'
import { useLanguageStore } from '@/pinia/modules/language'

const { t, locale } = useI18n()
const languageStore = useLanguageStore()

// 当前激活的标签页
const activeTab = ref('auth')

const config = ref({
  auth: {
    enableEmail: false,
    enableTelegram: false,
    enableQQ: false,
    enableOAuth2: false,
    enablePublicRegistration: false, // 是否启用公开注册
    emailSMTPHost: '',
    emailSMTPPort: 587,
    emailUsername: '',
    emailPassword: '',
    telegramBotToken: '',
    qqAppID: '',
    qqAppKey: ''
  },
  quota: {
    defaultLevel: 1,
    levelLimits: {
      1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 1024, bandwidth: 100 }, maxTraffic: 102400, expiryDays: 30 },    // 磁盘1GB, 流量100MB
      2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 2048, bandwidth: 200 }, maxTraffic: 204800, expiryDays: 30 },   // 磁盘2GB, 流量200MB  
      3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 4096, bandwidth: 500 }, maxTraffic: 409600, expiryDays: 30 },   // 磁盘4GB, 流量400MB
      4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 8192, bandwidth: 1000 }, maxTraffic: 819200, expiryDays: 30 },  // 磁盘8GB, 流量800MB
      5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 16384, bandwidth: 2000 }, maxTraffic: 1638400, expiryDays: 30 } // 磁盘16GB, 流量1600MB
    }
  },
  inviteCode: {
    enabled: false
  },
  other: {
    maxAvatarSize: 2, // MB
    defaultLanguage: '' // 默认语言，空字符串表示使用浏览器语言
  }
})

const instanceTypePermissions = ref({
  minLevelForContainer: 1,
  minLevelForVM: 3,
  minLevelForDeleteContainer: 1,
  minLevelForDeleteVM: 2,
  minLevelForResetContainer: 1,
  minLevelForResetVM: 2
})

const loading = ref(false)

// 记录系统配置的语言，用于判断是否修改
const systemConfigLanguage = ref('')

const loadConfig = async () => {
  loading.value = true
  try {
    const response = await getAdminConfig()
    console.log('加载配置响应:', response)
    console.log('配置数据:', response.data)
    if (response.code === 0 && response.data) {
      // 合并配置，确保所有字段都有默认值
      if (response.data.auth) {
        config.value.auth = {
          ...config.value.auth,
          ...response.data.auth
        }
      }
      
      if (response.data.inviteCode) {
        config.value.inviteCode = {
          ...config.value.inviteCode,
          ...response.data.inviteCode
        }
      }

      // 加载其他配置
      if (response.data.other) {
        console.log('加载其他配置:', response.data.other)
        config.value.other = {
          ...config.value.other,
          ...response.data.other
        }
        // 记录当前的系统语言配置
        systemConfigLanguage.value = config.value.other.defaultLanguage || ''
        console.log('合并后的其他配置:', config.value.other)
        console.log('当前系统语言配置:', systemConfigLanguage.value)
      }
      
      // 加载等级配置
      if (response.data.quota && response.data.quota.levelLimits) {
        config.value.quota.levelLimits = {}
        for (let level = 1; level <= 5; level++) {
          const levelKey = String(level)
          if (response.data.quota.levelLimits[levelKey]) {
            const limitData = response.data.quota.levelLimits[levelKey]
            config.value.quota.levelLimits[level] = {
              maxInstances: limitData['max-instances'] || (level * 2),
              maxResources: {
                cpu: limitData['max-resources']?.cpu || (level * 2),
                memory: limitData['max-resources']?.memory || (1024 * Math.pow(2, level - 1)),
                disk: limitData['max-resources']?.disk || (10240 * Math.pow(2, level - 1)),
                bandwidth: limitData['max-resources']?.bandwidth || (10 * level)
              },
              maxTraffic: limitData['max-traffic'] || (1024 * level),
              expiryDays: limitData['expiry-days'] || 30
            }
          } else {
            // 如果没有数据，使用默认值
            config.value.quota.levelLimits[level] = {
              maxInstances: level * 2,
              maxResources: {
                cpu: level * 2,
                memory: 1024 * Math.pow(2, level - 1),
                disk: 10240 * Math.pow(2, level - 1),
                bandwidth: 10 * level
              },
              maxTraffic: 1024 * level,
              expiryDays: 30
            }
          }
        }
      }
    }
  } catch (error) {
    console.error('加载配置失败:', error)
    ElMessage.error(t('admin.config.loadConfigFailed'))
  } finally {
    loading.value = false
  }
}

const loadInstanceTypePermissions = async () => {
  try {
    const response = await getInstanceTypePermissions()
    console.log('加载实例类型权限配置响应:', response)
    if (response.code === 0 && response.data) {
      instanceTypePermissions.value = {
        minLevelForContainer: response.data.minLevelForContainer || 1,
        minLevelForVM: response.data.minLevelForVM || 3,
        minLevelForDeleteContainer: response.data.minLevelForDeleteContainer || 1,
        minLevelForDeleteVM: response.data.minLevelForDeleteVM || 2,
        minLevelForResetContainer: response.data.minLevelForResetContainer || 1,
        minLevelForResetVM: response.data.minLevelForResetVM || 2
      }
    }
  } catch (error) {
    console.error('加载实例类型权限配置失败:', error)
    ElMessage.error(t('admin.config.loadPermissionsFailed'))
  }
}

const saveConfig = async () => {
  // 验证配置数据，确保所有资源限制值不为空
  for (let level = 1; level <= 5; level++) {
    const limit = config.value.quota.levelLimits[level]
    if (!limit) {
      ElMessage.error(t('admin.config.levelConfigEmpty', { level }))
      return
    }
    
    // 验证必填字段
    if (!limit.maxInstances || limit.maxInstances <= 0) {
      ElMessage.error(t('admin.config.maxInstancesInvalid', { level }))
      return
    }
    
    if (!limit.maxTraffic || limit.maxTraffic <= 0) {
      ElMessage.error(t('admin.config.trafficLimitInvalid', { level }))
      return
    }
    
    if (!limit.maxResources) {
      ElMessage.error(t('admin.config.resourceConfigEmpty', { level }))
      return
    }
    
    // 验证各项资源限制
    if (!limit.maxResources.cpu || limit.maxResources.cpu <= 0) {
      ElMessage.error(t('admin.config.maxCPUInvalid', { level }))
      return
    }
    
    if (!limit.maxResources.memory || limit.maxResources.memory <= 0) {
      ElMessage.error(t('admin.config.maxMemoryInvalid', { level }))
      return
    }
    
    if (!limit.maxResources.disk || limit.maxResources.disk <= 0) {
      ElMessage.error(t('admin.config.maxDiskInvalid', { level }))
      return
    }
    
    if (!limit.maxResources.bandwidth || limit.maxResources.bandwidth <= 0) {
      ElMessage.error(t('admin.config.maxBandwidthInvalid', { level }))
      return
    }
  }
  
  loading.value = true
  try {
    console.log('开始保存配置...')
    console.log('基础配置:', config.value)
    console.log('实例类型权限配置:', instanceTypePermissions.value)
    console.log('语言配置:', config.value.other.defaultLanguage)
    
    // 记录修改前的语言设置
    const oldLanguage = systemConfigLanguage.value
    const newLanguage = config.value.other.defaultLanguage
    const languageChanged = oldLanguage !== newLanguage
    
    // 转换 levelLimits 为 kebab-case 格式（外层字段），max-resources 内部保持 camelCase
    const configToSave = JSON.parse(JSON.stringify(config.value))
    if (configToSave.quota && configToSave.quota.levelLimits) {
      const convertedLimits = {}
      Object.keys(configToSave.quota.levelLimits).forEach(level => {
        const limit = configToSave.quota.levelLimits[level]
        convertedLimits[level] = {
          'max-instances': limit.maxInstances,
          'max-resources': {
            cpu: limit.maxResources.cpu,
            memory: limit.maxResources.memory,
            disk: limit.maxResources.disk,
            bandwidth: limit.maxResources.bandwidth
          },
          'max-traffic': limit.maxTraffic,
          'expiry-days': limit.expiryDays || 30
        }
      })
      configToSave.quota.levelLimits = convertedLimits
    }
    
    // 保存基础配置
    const configResult = await updateAdminConfig(configToSave)
    console.log('基础配置保存结果:', configResult)
    
    // 保存实例类型权限配置
    const permissionsResult = await updateInstanceTypePermissions(instanceTypePermissions.value)
    console.log('实例类型权限配置保存结果:', permissionsResult)
    
    ElMessage.success(t('admin.config.saveSuccess'))
    
    // 如果修改了默认语言，强制应用并刷新页面
    if (languageChanged) {
      console.log('[Config] 系统语言已修改，从', oldLanguage, '到', newLanguage)
      
      // 更新 language store 中的系统配置语言并强制应用
      const effectiveLanguage = languageStore.forceApplySystemLanguage(newLanguage)
      console.log('[Config] 强制应用后的有效语言:', effectiveLanguage)
      
      // 更新当前页面的语言
      locale.value = effectiveLanguage
      
      // 显示通知，告知用户页面将刷新
      ElNotification({
        title: t('common.success'),
        message: t('admin.config.languageChangedRefreshing'),
        type: 'success',
        duration: 2000
      })
      
      // 延迟刷新页面，让用户看到通知
      setTimeout(() => {
        window.location.reload()
      }, 2000)
    } else {
      // 保存成功后重新加载配置，确保显示最新数据
      await loadConfig()
      await loadInstanceTypePermissions()
    }
  } catch (error) {
    console.error('保存配置失败:', error)
    ElMessage.error(t('admin.config.saveFailed', { error: error.message || t('common.unknownError') }))
  } finally {
    loading.value = false
  }
}

const resetConfig = async () => {
  await loadConfig()
  await loadInstanceTypePermissions()
  ElMessage.success(t('admin.config.configReset'))
}

onMounted(() => {
  loadConfig()
  loadInstanceTypePermissions()
})
</script>

<style scoped>
.config-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: #303133;
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
  border-color: #409eff;
  box-shadow: 0 2px 12px 0 rgba(64, 158, 255, 0.1);
}

.level-card.default-level {
  border-color: #67c23a;
  background-color: #f0f9ff;
}

.level-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.level-title {
  font-weight: 600;
  color: #303133;
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
  color: #606266;
}

.config-form :deep(.el-alert) {
  margin-bottom: 20px;
}

.form-item-hint {
  font-size: 12px;
  color: #909399;
  margin-top: 4px;
  line-height: 1.4;
}
</style>
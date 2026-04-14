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

        <!-- 实名认证配置 -->
        <el-tab-pane
          :label="$t('admin.config.kycConfig')"
          name="kyc"
        >
          <el-form
            v-loading="loading"
            :model="config"
            label-width="160px"
            class="config-form"
          >
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.kycMethod')">
                  <el-select v-model="config.kyc.method" style="width: 100%">
                    <el-option value="manual" :label="$t('admin.config.kycMethodManual')" />
                    <el-option value="alipay" :label="$t('admin.config.kycMethodAlipay')" />
                    <el-option value="both" :label="$t('admin.config.kycMethodBoth')" />
                  </el-select>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item :label="$t('admin.config.kycRequireRealName')">
                  <el-switch v-model="config.kyc.requireRealName" />
                  <div class="form-item-hint">{{ $t('admin.config.kycRequireRealNameHint') }}</div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-divider>{{ $t('admin.config.kycRestrictions') }}</el-divider>

            <el-row :gutter="20">
              <el-col :span="8">
                <el-form-item :label="$t('admin.config.kycRestrictCreateInstance')">
                  <el-switch v-model="config.kyc.restrictCreateInstance" />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item :label="$t('admin.config.kycRestrictRedeemCode')">
                  <el-switch v-model="config.kyc.restrictRedeemCode" />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item :label="$t('admin.config.kycRestrictDomainBind')">
                  <el-switch v-model="config.kyc.restrictDomainBind" />
                </el-form-item>
              </el-col>
            </el-row>

            <template v-if="config.kyc.method === 'alipay' || config.kyc.method === 'both'">
              <el-divider>{{ $t('admin.config.kycAlipayConfig') }}</el-divider>

              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item :label="$t('admin.config.kycAlipayAppId')">
                    <el-input v-model="config.kyc.alipayAppId" :placeholder="$t('admin.config.kycAlipayAppId')" />
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item :label="$t('admin.config.kycAlipayPrivateKey')">
                    <el-input v-model="config.kyc.alipayPrivateKey" type="password" show-password :placeholder="$t('admin.config.kycAlipayPrivateKey')" />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item :label="$t('admin.config.kycAlipayPublicKey')">
                    <el-input v-model="config.kyc.alipayPublicKey" type="password" show-password :placeholder="$t('admin.config.kycAlipayPublicKey')" />
                  </el-form-item>
                </el-col>
              </el-row>
            </template>
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
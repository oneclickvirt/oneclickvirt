<template>
  <div class="config-container">
    <el-card>
      <template #header>
        <div class="config-header">
          <span>系统配置</span>
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
          label="基础认证"
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
                <el-form-item label="邮箱登录">
                  <el-switch v-model="config.auth.enableEmail" />
                  <div class="form-item-hint">
                    启用后，用户可通过邮箱验证码登录
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  label="公开注册"
                  help="是否允许无邀请码注册"
                >
                  <el-switch v-model="config.auth.enablePublicRegistration" />
                </el-form-item>
              </el-col>
            </el-row>
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item label="Telegram登录">
                  <el-switch v-model="config.auth.enableTelegram" />
                  <div class="form-item-hint">
                    启用后，用户可通过Telegram验证码登录
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="QQ登录">
                  <el-switch v-model="config.auth.enableQQ" />
                  <div class="form-item-hint">
                    启用后，用户可通过QQ验证码登录
                  </div>
                </el-form-item>
              </el-col>
            </el-row>
            
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item label="OAuth2">
                  <el-switch v-model="config.auth.enableOAuth2" />
                  <div class="form-item-hint">
                    启用后，用户可通过OAuth2提供商注册或登录
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="邀请码系统">
                  <el-switch v-model="config.inviteCode.enabled" />
                  <div class="form-item-hint">
                    启用后，新用户注册需要提供有效的邀请码
                  </div>
                </el-form-item>
              </el-col>
            </el-row>
          </el-form>
        </el-tab-pane>

        <!-- 邮箱SMTP配置 -->
        <el-tab-pane
          label="邮箱配置"
          name="email"
        >
          <el-form
            v-loading="loading"
            :model="config"
            label-width="140px"
            class="config-form"
          >
            <el-alert
              title="SMTP配置说明"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              配置SMTP服务用于发送邮箱验证码和系统通知
            </el-alert>
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item label="SMTP主机">
                  <el-input
                    v-model="config.auth.emailSMTPHost"
                    placeholder="例如：smtp.gmail.com"
                  />
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="SMTP端口">
                  <el-input-number
                    v-model="config.auth.emailSMTPPort"
                    :min="1"
                    :max="65535"
                    placeholder="常用：587或465"
                    style="width: 100%"
                  />
                </el-form-item>
              </el-col>
            </el-row>
            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item label="邮箱用户名">
                  <el-input
                    v-model="config.auth.emailUsername"
                    placeholder="发送邮件的邮箱地址"
                  />
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="邮箱密码/授权码">
                  <el-input
                    v-model="config.auth.emailPassword"
                    type="password"
                    placeholder="邮箱密码或应用专用密码"
                    show-password
                  />
                </el-form-item>
              </el-col>
            </el-row>
          </el-form>
        </el-tab-pane>

        <!-- 第三方登录配置 -->
        <el-tab-pane
          label="第三方登录"
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
                  <span>Telegram 配置</span>
                  <el-switch v-model="config.auth.enableTelegram" />
                </div>
              </template>
              <el-form-item label="Bot Token">
                <el-input
                  v-model="config.auth.telegramBotToken"
                  placeholder="请输入 Telegram Bot Token"
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
                  <span>QQ 配置</span>
                  <el-switch v-model="config.auth.enableQQ" />
                </div>
              </template>
              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item label="App ID">
                    <el-input
                      v-model="config.auth.qqAppID"
                      placeholder="请输入 QQ App ID"
                      :disabled="!config.auth.enableQQ"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="App Key">
                    <el-input
                      v-model="config.auth.qqAppKey"
                      placeholder="请输入 QQ App Key"
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
          label="用户等级"
          name="quota"
        >
          <el-form
            v-loading="loading"
            :model="config"
            label-width="140px"
            class="config-form"
          >
            <el-alert
              title="用户等级说明"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              <div>配置不同用户等级的资源限制，等级越高可用资源越多。</div>
              <div style="margin-top: 8px; color: #67C23A;">
                <i class="el-icon-check"></i>
                配置保存时会自动同步所有用户的资源限制到对应等级配置，无需手动操作。
              </div>
              <div style="margin-top: 8px; color: #E6A23C;">
                <i class="el-icon-warning"></i>
                注意：所有资源限制值不能为空或小于等于0，清空输入框将无法保存配置。
              </div>
            </el-alert>
            
            <el-form-item label="新用户默认等级">
              <el-select
                v-model="config.quota.defaultLevel"
                placeholder="请选择默认用户等级"
                style="width: 200px"
              >
                <el-option
                  v-for="level in 5"
                  :key="level"
                  :label="`等级${level}`"
                  :value="level"
                />
              </el-select>
            </el-form-item>

            <el-divider content-position="left">
              等级限制配置
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
                      <span class="level-title">等级{{ level }}限制</span>
                      <el-tag
                        v-if="config.quota.defaultLevel === level"
                        type="success"
                        size="small"
                      >
                        默认等级
                      </el-tag>
                    </div>
                  </template>
                  <el-row :gutter="20">
                    <el-col :span="6">
                      <el-form-item label="最大实例数">
                        <el-input-number 
                          v-model="config.quota.levelLimits[level].maxInstances" 
                          :min="1" 
                          :max="100"
                          :controls="true"
                          :step="1"
                          style="width: 100%" 
                        />
                      </el-form-item>
                    </el-col>
                    <el-col :span="6">
                      <el-form-item label="最大CPU核心">
                        <el-input-number 
                          v-model="config.quota.levelLimits[level].maxResources.cpu" 
                          :min="1" 
                          :max="64"
                          :controls="true"
                          :step="1"
                          style="width: 100%" 
                        />
                      </el-form-item>
                    </el-col>
                    <el-col :span="6">
                      <el-form-item label="最大内存(MB)">
                        <el-input-number 
                          v-model="config.quota.levelLimits[level].maxResources.memory" 
                          :min="128" 
                          :max="65536"
                          :controls="true"
                          :step="128"
                          style="width: 100%" 
                        />
                      </el-form-item>
                    </el-col>
                    <el-col :span="6">
                      <el-form-item label="最大磁盘(MB)">
                        <el-input-number 
                          v-model="config.quota.levelLimits[level].maxResources.disk" 
                          :min="512" 
                          :max="102400"
                          :controls="true"
                          :step="512"
                          style="width: 100%" 
                        />
                      </el-form-item>
                    </el-col>
                  </el-row>
                  <el-row :gutter="20">
                    <el-col :span="6">
                      <el-form-item label="最大带宽(Mbps)">
                        <el-input-number 
                          v-model="config.quota.levelLimits[level].maxResources.bandwidth" 
                          :min="1" 
                          :max="10000"
                          :controls="true"
                          :step="1"
                          style="width: 100%" 
                        />
                      </el-form-item>
                    </el-col>
                    <el-col :span="6">
                      <el-form-item label="流量限制(MB)">
                        <el-input-number 
                          v-model="config.quota.levelLimits[level].maxTraffic" 
                          :min="1024" 
                          :max="10485760"
                          :controls="true"
                          :step="1024"
                          style="width: 100%" 
                        />
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
          label="实例权限"
          name="instancePermissions"
        >
          <el-form
            v-loading="loading"
            :model="instanceTypePermissions"
            label-width="140px"
            class="config-form"
          >
            <el-alert
              title="实例类型权限说明"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              配置不同实例类型和操作的最低用户等级要求。容器对所有等级用户开放，虚拟机和删除操作需要达到指定等级。
            </el-alert>
            
            <el-row :gutter="20">
              <el-col :span="8">
                <el-form-item label="容器权限">
                  <el-tag
                    type="success"
                    size="large"
                  >
                    所有等级用户均可创建
                  </el-tag>
                  <div class="form-item-hint">
                    容器资源占用较少，对所有用户开放
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="虚拟机最低等级">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForVM"
                    placeholder="选择等级"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="`等级 ${level}`"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    虚拟机需要更多资源，建议设置适当门槛
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="删除实例最低等级">
                  <el-select
                    v-model="instanceTypePermissions.minLevelForDelete"
                    placeholder="选择等级"
                    style="width: 100%"
                  >
                    <el-option
                      v-for="level in [1, 2, 3, 4, 5]"
                      :key="level"
                      :label="`等级 ${level}`"
                      :value="level"
                    />
                  </el-select>
                  <div class="form-item-hint">
                    低等级用户需管理员协助删除，避免误操作
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-alert
              title="权限设置建议"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 20px;"
            >
              <ul style="margin: 0; padding-left: 20px;">
                <li>容器：资源占用较少，对所有等级开放</li>
                <li>虚拟机：资源占用较大，建议设置等级2-3以上</li>
                <li>删除实例：建议设置等级2以上，避免新用户误删实例</li>
              </ul>
            </el-alert>
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
          保存当前配置
        </el-button>
        <el-button 
          size="large"
          @click="resetConfig"
        >
          重置配置
        </el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getAdminConfig, updateAdminConfig } from '@/api/config'
import { getInstanceTypePermissions, updateInstanceTypePermissions } from '@/api/admin'

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
      1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 10 }, maxTraffic: 1024 },    // 10GB = 10240MB, 流量1GB
      2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 20 }, maxTraffic: 2048 },   // 20GB = 20480MB, 流量2GB  
      3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 50 }, maxTraffic: 4096 },   // 40GB = 40960MB, 流量4GB
      4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 100 }, maxTraffic: 8192 },  // 80GB = 81920MB, 流量8GB
      5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 200 }, maxTraffic: 16384 } // 160GB = 163840MB, 流量16GB
    }
  },
  inviteCode: {
    enabled: false
  }
})

const instanceTypePermissions = ref({
  minLevelForContainer: 1, // 固定为1，所有等级用户都可创建
  minLevelForVM: 3,
  minLevelForDelete: 2 // 等级2及以上可以自行删除实例
})

const loading = ref(false)

const loadConfig = async () => {
  loading.value = true
  try {
    const response = await getAdminConfig()
    if (response.data) {
      // 直接使用服务端返回的完整配置
      if (response.data.auth) {
        config.value.auth = {
          ...config.value.auth,
          ...response.data.auth
        }
      }
      
      if (response.data.quota) {
        // 直接使用服务端返回的配额配置
        config.value.quota = response.data.quota
        
        // 确保所有5个等级都存在（如果服务端没有返回某个等级，填充默认值）
        if (!config.value.quota.levelLimits) {
          config.value.quota.levelLimits = {}
        }
        for (let i = 1; i <= 5; i++) {
          if (!config.value.quota.levelLimits[i]) {
            config.value.quota.levelLimits[i] = {
              maxInstances: i,
              maxResources: {
                cpu: Math.pow(2, i - 1),
                memory: 512 * Math.pow(2, i - 1),
                disk: 10240 * Math.pow(2, i - 1),
                bandwidth: 10 * i
              },
              maxTraffic: 1024 * i
            }
          }
        }
      }
      
      if (response.data.inviteCode) {
        config.value.inviteCode = {
          ...config.value.inviteCode,
          ...response.data.inviteCode
        }
      }
    }
  } catch (error) {
    console.error('加载配置失败:', error)
    ElMessage.error('加载配置失败')
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
        minLevelForDelete: response.data.minLevelForDelete || 2
      }
      console.log('实例类型权限配置已加载:', instanceTypePermissions.value)
    }
  } catch (error) {
    console.error('加载实例类型权限配置失败:', error)
    ElMessage.error('加载实例类型权限配置失败')
  }
}

const saveConfig = async () => {
  // 验证配置数据，确保所有资源限制值不为空
  for (let level = 1; level <= 5; level++) {
    const limit = config.value.quota.levelLimits[level]
    if (!limit) {
      ElMessage.error(`等级${level}的配置不能为空`)
      return
    }
    
    // 验证必填字段
    if (!limit.maxInstances || limit.maxInstances <= 0) {
      ElMessage.error(`等级${level}的最大实例数不能为空或小于等于0`)
      return
    }
    
    if (!limit.maxTraffic || limit.maxTraffic <= 0) {
      ElMessage.error(`等级${level}的流量限制不能为空或小于等于0`)
      return
    }
    
    if (!limit.maxResources) {
      ElMessage.error(`等级${level}的资源配置不能为空`)
      return
    }
    
    // 验证各项资源限制
    if (!limit.maxResources.cpu || limit.maxResources.cpu <= 0) {
      ElMessage.error(`等级${level}的最大CPU核心数不能为空或小于等于0`)
      return
    }
    
    if (!limit.maxResources.memory || limit.maxResources.memory <= 0) {
      ElMessage.error(`等级${level}的最大内存不能为空或小于等于0`)
      return
    }
    
    if (!limit.maxResources.disk || limit.maxResources.disk <= 0) {
      ElMessage.error(`等级${level}的最大磁盘不能为空或小于等于0`)
      return
    }
    
    if (!limit.maxResources.bandwidth || limit.maxResources.bandwidth <= 0) {
      ElMessage.error(`等级${level}的最大带宽不能为空或小于等于0`)
      return
    }
  }
  
  loading.value = true
  try {
    console.log('开始保存配置...')
    console.log('基础配置:', config.value)
    console.log('实例类型权限配置:', instanceTypePermissions.value)
    
    // 保存基础配置
    const configResult = await updateAdminConfig(config.value)
    console.log('基础配置保存结果:', configResult)
    
    // 保存实例类型权限配置
    const permissionsResult = await updateInstanceTypePermissions(instanceTypePermissions.value)
    console.log('实例类型权限配置保存结果:', permissionsResult)
    
    ElMessage.success('配置保存成功，已自动同步用户资源限制')
    
    // 保存成功后重新加载配置，确保显示最新数据
    await loadConfig()
    await loadInstanceTypePermissions()
  } catch (error) {
    console.error('保存配置失败:', error)
    ElMessage.error('配置保存失败: ' + (error.message || '未知错误'))
  } finally {
    loading.value = false
  }
}

const resetConfig = async () => {
  await loadConfig()
  await loadInstanceTypePermissions()
  ElMessage.success('配置已重置')
}

onMounted(() => {
  loadConfig()
  loadInstanceTypePermissions()
})
</script>

<style scoped>
.config-container {
  padding: 20px;
}

.config-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
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

/* 标签页样式优化 */
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

/* 表单样式优化 */
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
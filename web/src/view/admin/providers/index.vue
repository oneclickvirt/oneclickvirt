<template>
  <div class="providers-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.providers.title') }}</span>
          <el-button
            type="primary"
            @click="showAddDialog = true"
          >
            {{ $t('admin.providers.addProvider') }}
          </el-button>
        </div>
      </template>
      
      <!-- 搜索过滤 -->
      <div class="filter-container">
        <el-row :gutter="20">
          <el-col :span="6">
            <el-input
              v-model="searchForm.name"
              :placeholder="$t('admin.providers.searchByName')"
              clearable
              @clear="handleSearch"
              @keyup.enter="handleSearch"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.type"
              :placeholder="$t('admin.providers.selectType')"
              clearable
              @change="handleSearch"
            >
              <el-option
                :label="$t('admin.providers.proxmox')"
                value="proxmox"
              />
              <el-option
                :label="$t('admin.providers.lxd')"
                value="lxd"
              />
              <el-option
                :label="$t('admin.providers.incus')"
                value="incus"
              />
              <el-option
                :label="$t('admin.providers.docker')"
                value="docker"
              />
            </el-select>
          </el-col>
          <el-col :span="4">
            <el-select
              v-model="searchForm.status"
              :placeholder="$t('admin.providers.selectStatus')"
              clearable
              @change="handleSearch"
            >
              <el-option
                :label="$t('admin.providers.statusActive')"
                value="active"
              />
              <el-option
                :label="$t('admin.providers.statusOffline')"
                value="offline"
              />
              <el-option
                :label="$t('admin.providers.statusFrozen')"
                value="frozen"
              />
            </el-select>
          </el-col>
          <el-col :span="6">
            <el-button
              type="primary"
              @click="handleSearch"
            >
              {{ $t('admin.providers.search') }}
            </el-button>
            <el-button @click="handleReset">
              {{ $t('admin.providers.reset') }}
            </el-button>
          </el-col>
        </el-row>
      </div>
      
      <el-table
        v-loading="loading"
        :data="providers"
        style="width: 100%"
      >
        <el-table-column
          prop="id"
          label="ID"
          width="60"
        />
        <el-table-column
          prop="name"
          :label="$t('common.name')"
        />
        <el-table-column
          prop="type"
          :label="$t('admin.providers.providerType')"
        />
        <el-table-column
          :label="$t('admin.providers.location')"
          width="120"
        >
          <template #default="scope">
            <div class="location-cell">
              <span
                v-if="scope.row.countryCode"
                class="flag-icon"
              >{{ getFlagEmoji(scope.row.countryCode) }}</span>
              <span>{{ formatLocation(scope.row) }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.apiEndpoint')"
          width="140"
        >
          <template #default="scope">
            {{ scope.row.endpoint ? scope.row.endpoint.split(':')[0] : '-' }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.sshPort')"
          width="80"
        >
          <template #default="scope">
            {{ scope.row.sshPort || 22 }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.supportTypes')"
          width="120"
        >
          <template #default="scope">
            <div class="support-types">
              <el-tag
                v-if="scope.row.container_enabled"
                size="small"
                type="primary"
              >
                {{ $t('admin.providers.container') }}
              </el-tag>
              <el-tag
                v-if="scope.row.vm_enabled"
                size="small"
                type="success"
              >
                {{ $t('admin.providers.vm') }}
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="architecture"
          :label="$t('admin.providers.architecture')"
          width="80"
        >
          <template #default="scope">
            <el-tag
              size="small"
              type="info"
            >
              {{ scope.row.architecture || 'amd64' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.storagePool')"
          width="100"
        >
          <template #default="scope">
            <el-tag
              v-if="scope.row.type === 'proxmox' && scope.row.storagePool"
              size="small"
              type="warning"
            >
              <el-icon style="margin-right: 4px;"><FolderOpened /></el-icon>
              {{ scope.row.storagePool }}
            </el-tag>
            <el-text
              v-else-if="scope.row.type === 'proxmox'"
              size="small"
              type="info"
            >
              {{ $t('admin.providers.notConfigured') }}
            </el-text>
            <el-text
              v-else
              size="small"
              type="info"
            >
              -
            </el-text>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.connectionStatus')"
          width="90"
        >
          <template #default="scope">
            <div class="connection-status">
              <div style="margin-bottom: 4px;">
                <el-tag 
                  size="small" 
                  :type="getStatusType(scope.row.apiStatus)"
                >
                  API: {{ getStatusText(scope.row.apiStatus) }}
                </el-tag>
              </div>
              <div>
                <el-tag 
                  size="small" 
                  :type="getStatusType(scope.row.sshStatus)"
                >
                  SSH: {{ getStatusText(scope.row.sshStatus) }}
                </el-tag>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.expiryTime')"
          width="120"
        >
          <template #default="scope">
            <div v-if="scope.row.expiresAt">
              <el-tag 
                :type="isExpired(scope.row.expiresAt) ? 'danger' : isNearExpiry(scope.row.expiresAt) ? 'warning' : 'success'" 
                size="small"
              >
                {{ formatDateTime(scope.row.expiresAt) }}
              </el-tag>
            </div>
            <el-text
              v-else
              size="small"
              type="info"
            >
              {{ $t('admin.providers.neverExpires') }}
            </el-text>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.trafficUsage')"
          width="130"
        >
          <template #default="scope">
            <div class="traffic-info">
              <div class="traffic-usage">
                <span>{{ formatTraffic(scope.row.usedTraffic) }}</span>
                <span class="separator">/</span>
                <span>{{ formatTraffic(scope.row.maxTraffic) }}</span>
              </div>
              <div class="traffic-progress">
                <el-progress
                  :percentage="getTrafficPercentage(scope.row.usedTraffic, scope.row.maxTraffic)"
                  :status="scope.row.trafficLimited ? 'exception' : getTrafficProgressStatus(scope.row.usedTraffic, scope.row.maxTraffic)"
                  :stroke-width="6"
                  :show-text="false"
                />
              </div>
              <div
                v-if="scope.row.trafficLimited"
                class="traffic-status"
              >
                <el-tag
                  type="danger"
                  size="small"
                >
                  {{ $t('admin.providers.trafficExceeded') }}
                </el-tag>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.serverStatus')"
          width="100"
        >
          <template #default="scope">
            <el-tag
              v-if="scope.row.isFrozen"
              type="danger"
              size="small"
            >
              {{ $t('admin.providers.frozen') }}
            </el-tag>
            <el-tag
              v-else-if="isExpired(scope.row.expiresAt)"
              type="warning"
              size="small"
            >
              {{ $t('admin.providers.expired') }}
            </el-tag>
            <el-tag
              v-else
              type="success"
              size="small"
            >
              {{ $t('common.normal') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.nodeResources')"
          width="120"
        >
          <template #default="scope">
            <div
              v-if="scope.row.resourceSynced"
              class="resource-info"
            >
              <div class="resource-item">
                <el-icon><Cpu /></el-icon>
                <span>{{ scope.row.nodeCpuCores || 0 }} {{ $t('admin.providers.cores') }}</span>
              </div>
              <div class="resource-item">
                <el-icon><Monitor /></el-icon>
                <span>{{ formatMemorySize(scope.row.nodeMemoryTotal) }}</span>
                <span v-if="scope.row.nodeSwapTotal > 0">+{{ formatMemorySize(scope.row.nodeSwapTotal) }}S</span>
              </div>
              <div class="resource-item">
                <el-icon><FolderOpened /></el-icon>
                <span>{{ formatDiskSize(scope.row.nodeDiskTotal) }} {{ $t('admin.providers.totalSpace') }}</span>
              </div>
              <div class="sync-time">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ formatRelativeTime(scope.row.resourceSyncedAt) }}
                </el-text>
              </div>
            </div>
            <div
              v-else
              class="resource-placeholder"
            >
              <el-text
                size="small"
                type="info"
              >
                <el-icon><Loading /></el-icon>
                {{ $t('admin.providers.notSynced') }}
              </el-text>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.providers.taskStatus')"
          width="120"
        >
          <template #default="scope">
            <div class="task-status">
              <div style="margin-bottom: 4px;">
                <el-text size="small">
                  {{ $t('admin.providers.instances') }}: {{ scope.row.instanceCount || 0 }}
                </el-text>
              </div>
              <div style="margin-bottom: 4px;">
                <el-text size="small">
                  {{ $t('admin.providers.runningTasks') }}: {{ scope.row.runningTasksCount || 0 }}
                </el-text>
              </div>
              <div>
                <el-tag
                  v-if="scope.row.allowConcurrentTasks"
                  type="success"
                  size="small"
                >
                  {{ $t('admin.providers.concurrent') }} ({{ scope.row.maxConcurrentTasks }})
                </el-tag>
                <el-tag
                  v-else
                  type="warning"
                  size="small"
                >
                  {{ $t('admin.providers.serial') }}
                </el-tag>
              </div>
              <div style="margin-top: 4px;">
                <el-tag
                  v-if="scope.row.enableTaskPolling"
                  type="primary"
                  size="small"
                >
                  {{ $t('admin.providers.polling') }} {{ scope.row.taskPollInterval }}s
                </el-tag>
                <el-tag
                  v-else
                  type="info"
                  size="small"
                >
                  {{ $t('admin.providers.pollingDisabled') }}
                </el-tag>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="290"
          fixed="right"
        >
          <template #default="scope">
            <div class="table-action-buttons">
              <a
                class="table-action-link"
                @click="editProvider(scope.row)"
              >
                {{ $t('common.edit') }}
              </a>
              <a 
                v-if="(scope.row.type === 'lxd' || scope.row.type === 'incus' || scope.row.type === 'proxmox')" 
                class="table-action-link" 
                @click="autoConfigureAPI(scope.row)"
              >
                {{ $t('admin.providers.autoConfigureAPI') }}
              </a>
              <a 
                class="table-action-link" 
                @click="checkHealth(scope.row.id)"
              >
                {{ $t('admin.providers.healthCheck') }}
              </a>
              <a 
                v-if="scope.row.isFrozen" 
                class="table-action-link success" 
                @click="unfreezeServer(scope.row)"
              >
                {{ $t('admin.providers.unfreeze') }}
              </a>
              <a 
                v-else 
                class="table-action-link warning" 
                @click="freezeServer(scope.row.id)"
              >
                {{ $t('admin.providers.freeze') }}
              </a>
              <a
                class="table-action-link danger"
                @click="handleDeleteProvider(scope.row.id)"
              >
                {{ $t('common.delete') }}
              </a>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-wrapper">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>

    <!-- 添加/编辑服务器对话框 -->
    <el-dialog 
      v-model="showAddDialog" 
      :title="isEditing ? $t('admin.providers.editServer') : $t('admin.providers.addServer')" 
      width="800px"
      :close-on-click-modal="false"
    >
      <!-- 配置分类标签页 -->
      <el-tabs
        v-model="activeConfigTab"
        type="border-card"
        class="server-config-tabs"
        :lazy="false"
      >
        <!-- 基本信息 -->
        <el-tab-pane
          :label="$t('admin.providers.basicInfo')"
          name="basic"
        >
          <el-form
            ref="addProviderFormRef"
            :model="addProviderForm"
            :rules="addProviderRules"
            label-width="120px"
            class="server-form"
          >
            <el-form-item
              :label="$t('admin.providers.serverName')"
              prop="name"
            >
              <el-input
                v-model="addProviderForm.name"
                :placeholder="$t('admin.providers.serverNamePlaceholder')"
                maxlength="7"
                show-word-limit
              />
            </el-form-item>
            <el-form-item
              :label="$t('admin.providers.serverType')"
              prop="type"
            >
              <el-select
                v-model="addProviderForm.type"
                :placeholder="$t('admin.providers.serverTypePlaceholder')"
              >
                <el-option
                  label="Docker"
                  value="docker"
                />
                <el-option
                  label="LXD"
                  value="lxd"
                />
                <el-option
                  label="Incus"
                  value="incus"
                />
                <el-option
                  label="Proxmox"
                  value="proxmox"
                />
              </el-select>
            </el-form-item>
            <el-form-item
              :label="$t('admin.providers.hostAddress')"
              prop="host"
            >
              <el-input
                v-model="addProviderForm.host"
                :placeholder="$t('admin.providers.hostPlaceholder')"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.hostTip') }}
                </el-text>
              </div>
            </el-form-item>
            <el-form-item
              :label="$t('admin.providers.portIP')"
              prop="portIP"
            >
              <el-input
                v-model="addProviderForm.portIP"
                :placeholder="$t('admin.providers.portIPPlaceholder')"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.portIPTip') }}
                </el-text>
              </div>
            </el-form-item>
            <el-form-item
              :label="$t('admin.providers.port')"
              prop="port"
            >
              <el-input-number
                v-model="addProviderForm.port"
                :min="1"
                :max="65535"
                :controls="false"
              />
            </el-form-item>
            <el-form-item
              :label="$t('common.description')"
              prop="description"
            >
              <el-input 
                v-model="addProviderForm.description" 
                type="textarea" 
                :rows="3"
                :placeholder="$t('admin.providers.descriptionPlaceholder')"
              />
            </el-form-item>
            <el-form-item
              :label="$t('common.status')"
              prop="status"
            >
              <el-select
                v-model="addProviderForm.status"
                :placeholder="$t('admin.providers.statusPlaceholder')"
              >
                <el-option
                  :label="$t('common.enabled')"
                  value="active"
                />
                <el-option
                  :label="$t('common.disabled')"
                  value="inactive"
                />
              </el-select>
            </el-form-item>
            <el-form-item
              :label="$t('admin.providers.architecture')"
              prop="architecture"
            >
              <el-select
                v-model="addProviderForm.architecture"
                :placeholder="$t('admin.providers.architecturePlaceholder')"
              >
                <el-option
                  label="amd64 (x86_64)"
                  value="amd64"
                />
                <el-option
                  label="arm64 (aarch64)"
                  value="arm64"
                />
                <el-option
                  label="s390x (IBM Z)"
                  value="s390x"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.architectureTip') }}
                </el-text>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 连接配置 -->
        <el-tab-pane
          :label="$t('admin.providers.connectionConfig')"
          name="connection"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-form-item
              :label="$t('admin.providers.username')"
              prop="username"
            >
              <el-input
                v-model="addProviderForm.username"
                :placeholder="$t('admin.providers.usernamePlaceholder')"
              />
            </el-form-item>
            
            <!-- 认证方式选择（创建和编辑都显示） -->
            <el-form-item
              :label="$t('admin.providers.authMethod')"
              prop="authMethod"
            >
              <el-radio-group 
                v-model="addProviderForm.authMethod"
                @change="handleAuthMethodChange"
              >
                <el-radio-button label="password">
                  {{ $t('admin.providers.usePassword') }}
                </el-radio-button>
                <el-radio-button label="sshKey">
                  {{ $t('admin.providers.useSSHKey') }}
                </el-radio-button>
              </el-radio-group>
            </el-form-item>
            
            <!-- 密码认证 -->
            <el-form-item
              v-if="addProviderForm.authMethod === 'password'"
              :label="$t('admin.providers.password')"
              prop="password"
            >
              <el-input 
                v-model="addProviderForm.password" 
                type="password" 
                :placeholder="isEditing ? $t('admin.providers.passwordEditPlaceholder') : $t('admin.providers.passwordPlaceholder')" 
                show-password 
              />
              <div 
                v-if="isEditing"
                class="form-tip"
              >
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.passwordKeepTip') }}
                </el-text>
              </div>
            </el-form-item>
            
            <!-- SSH密钥认证 -->
            <el-form-item
              v-if="addProviderForm.authMethod === 'sshKey'"
              :label="$t('admin.providers.sshKey')"
              prop="sshKey"
            >
              <el-input 
                v-model="addProviderForm.sshKey" 
                type="textarea" 
                :rows="4"
                :placeholder="isEditing ? $t('admin.providers.sshKeyEditPlaceholder') : $t('admin.providers.sshKeyPlaceholder')"
              />
              <div 
                v-if="isEditing"
                class="form-tip"
              >
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.sshKeyEditTip') }}
                </el-text>
              </div>
            </el-form-item>
            
            <el-divider content-position="left">
              {{ $t('admin.providers.sshTimeoutConfig') }}
            </el-divider>
            
            <el-form-item
              :label="$t('admin.providers.connectTimeout')"
              prop="sshConnectTimeout"
            >
              <el-input-number
                v-model="addProviderForm.sshConnectTimeout"
                :min="5"
                :max="300"
                :step="5"
                :controls="false"
                placeholder="30"
              />
              <span style="margin-left: 10px;">{{ $t('admin.providers.seconds') }}</span>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.connectTimeoutTip') }}
                </el-text>
              </div>
            </el-form-item>
            
            <el-form-item
              :label="$t('admin.providers.executeTimeout')"
              prop="sshExecuteTimeout"
            >
              <el-input-number
                v-model="addProviderForm.sshExecuteTimeout"
                :min="30"
                :max="3600"
                :step="30"
                :controls="false"
                placeholder="300"
              />
              <span style="margin-left: 10px;">{{ $t('admin.providers.seconds') }}</span>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.executeTimeoutTip') }}
                </el-text>
              </div>
            </el-form-item>
            
            <el-form-item :label="$t('admin.providers.connectionTest')">
              <el-button
                type="primary"
                :loading="testingConnection"
                :disabled="!addProviderForm.host || !addProviderForm.username || (addProviderForm.authMethod === 'password' ? !addProviderForm.password : !addProviderForm.sshKey)"
                @click="testSSHConnection"
              >
                <el-icon v-if="!testingConnection">
                  <Connection />
                </el-icon>
                {{ testingConnection ? $t('admin.providers.testing') : $t('admin.providers.testSSH') }}
              </el-button>
              <div
                v-if="connectionTestResult"
                class="form-tip"
                style="margin-top: 10px;"
              >
                <el-alert
                  :title="connectionTestResult.title"
                  :type="connectionTestResult.type"
                  :closable="false"
                  show-icon
                >
                  <template v-if="connectionTestResult.success">
                    <div style="margin-top: 8px;">
                      <p><strong>{{ $t('admin.providers.testResults') }}:</strong></p>
                      <p>{{ $t('admin.providers.minLatency') }}: {{ connectionTestResult.minLatency }}ms</p>
                      <p>{{ $t('admin.providers.maxLatency') }}: {{ connectionTestResult.maxLatency }}ms</p>
                      <p>{{ $t('admin.providers.avgLatency') }}: {{ connectionTestResult.avgLatency }}ms</p>
                      <p style="margin-top: 8px;">
                        <strong>{{ $t('admin.providers.recommendedTimeout') }}: {{ connectionTestResult.recommendedTimeout }}{{ $t('common.seconds') }}</strong>
                      </p>
                      <el-button
                        type="primary"
                        size="small"
                        style="margin-top: 8px;"
                        @click="applyRecommendedTimeout"
                      >
                        {{ $t('admin.providers.applyRecommended') }}
                      </el-button>
                    </div>
                  </template>
                  <template v-else>
                    <p>{{ connectionTestResult.error }}</p>
                  </template>
                </el-alert>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 地理位置 -->
        <el-tab-pane
          :label="$t('admin.providers.location')"
          name="location"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-form-item
              :label="$t('admin.providers.region')"
              prop="region"
            >
              <el-input
                v-model="addProviderForm.region"
                :placeholder="$t('admin.providers.regionPlaceholder')"
              />
            </el-form-item>
            <el-form-item
              :label="$t('admin.providers.country')"
              prop="country"
            >
              <el-select 
                v-model="addProviderForm.country" 
                :placeholder="$t('admin.providers.countryPlaceholder')"
                filterable
                @change="onCountryChange"
              >
                <el-option-group
                  v-for="(regionCountries, region) in groupedCountries"
                  :key="region"
                  :label="region"
                >
                  <el-option 
                    v-for="country in regionCountries" 
                    :key="country.code" 
                    :label="`${country.flag} ${country.name}`" 
                    :value="country.name"
                  />
                </el-option-group>
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.countryTip') }}
                </el-text>
              </div>
            </el-form-item>
            <el-form-item
              :label="$t('admin.providers.city')"
              prop="city"
            >
              <el-input
                v-model="addProviderForm.city"
                :placeholder="$t('admin.providers.cityPlaceholder')"
                clearable
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.cityTip') }}
                </el-text>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 虚拟化配置 -->
        <el-tab-pane
          :label="$t('admin.providers.virtualizationConfig')"
          name="virtualization"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <!-- 虚拟化配置 -->
            <el-divider content-position="left">
              <el-icon><Monitor /></el-icon>
              <span style="margin-left: 8px;">{{ $t('admin.providers.virtualizationConfig') }}</span>
            </el-divider>

            <el-row :gutter="20" style="margin-bottom: 20px;">
              <el-col :span="12">
                <el-card shadow="hover" style="height: 100%;">
                  <template #header>
                    <div style="display: flex; align-items: center; font-weight: 600;">
                      <el-icon size="18" style="margin-right: 8px;"><Box /></el-icon>
                      <span>{{ $t('admin.providers.supportTypes') }}</span>
                    </div>
                  </template>
                  <div class="support-type-group" style="padding: 10px 0;">
                    <el-checkbox v-model="addProviderForm.containerEnabled" style="margin-right: 30px;">
                      <span style="font-size: 14px;">{{ $t('admin.providers.supportContainer') }}</span>
                      <el-tooltip :content="$t('admin.providers.containerTech')" placement="top">
                        <el-icon style="margin-left: 5px;"><InfoFilled /></el-icon>
                      </el-tooltip>
                    </el-checkbox>
                    <el-checkbox 
                      v-model="addProviderForm.vmEnabled"
                      :disabled="addProviderForm.type === 'docker'"
                    >
                      <span style="font-size: 14px;">{{ $t('admin.providers.supportVM') }}</span>
                      <el-tooltip :content="$t('admin.providers.vmTech')" placement="top">
                        <el-icon style="margin-left: 5px;"><InfoFilled /></el-icon>
                      </el-tooltip>
                    </el-checkbox>
                  </div>
                  <div class="form-tip" style="margin-top: 10px;">
                    <el-text size="small" type="info">
                      {{ addProviderForm.type === 'docker' ? $t('admin.providers.dockerOnlyContainer') : $t('admin.providers.selectVirtualizationType') }}
                    </el-text>
                  </div>
                </el-card>
              </el-col>
              <el-col :span="12">
                <el-card shadow="hover" style="height: 100%;">
                  <template #header>
                    <div style="display: flex; align-items: center; font-weight: 600;">
                      <el-icon size="18" style="margin-right: 8px;"><DocumentCopy /></el-icon>
                      <span>{{ $t('admin.providers.instanceLimits') }}</span>
                    </div>
                  </template>
                  <div style="padding: 5px 0;">
                    <el-form-item :label="$t('admin.providers.maxContainers')" label-width="100px" style="margin-bottom: 15px;">
                      <el-input-number
                        v-model="addProviderForm.maxContainerInstances"
                        :min="0"
                        :max="1000"
                        :step="1"
                        :controls="false"
                        :placeholder="$t('admin.providers.zeroUnlimited')"
                        size="small"
                        style="width: 100%"
                      />
                      <div class="form-tip" style="margin-top: 5px;">
                        <el-text size="small" type="info">{{ $t('admin.providers.maxContainersTip') }}</el-text>
                      </div>
                    </el-form-item>
                    
                    <el-form-item :label="$t('admin.providers.maxVMs')" label-width="100px" style="margin-bottom: 0;">
                      <el-input-number
                        v-model="addProviderForm.maxVMInstances"
                        :min="0"
                        :max="1000"
                        :step="1"
                        :controls="false"
                        :placeholder="$t('admin.providers.zeroUnlimited')"
                        size="small"
                        style="width: 100%"
                      />
                      <div class="form-tip" style="margin-top: 5px;">
                        <el-text size="small" type="info">{{ $t('admin.providers.maxVMsTip') }}</el-text>
                      </div>
                    </el-form-item>
                  </div>
                </el-card>
              </el-col>
            </el-row>

            <!-- 容器资源限制配置 -->
            <div style="margin-top: 20px;">
              <el-card shadow="hover">
                <template #header>
                  <div style="display: flex; align-items: center; justify-content: space-between;">
                    <div style="display: flex; align-items: center; font-weight: 600;">
                      <el-icon size="18" style="margin-right: 8px;"><Box /></el-icon>
                      <span>{{ $t('admin.providers.containerResourceLimits') }}</span>
                    </div>
                    <el-tag size="small" type="info">Container</el-tag>
                  </div>
                </template>
                <el-alert
                  type="warning"
                  :closable="false"
                  show-icon
                  style="margin-bottom: 20px;"
                >
                  <template #title>
                    <span style="font-size: 13px;">{{ $t('admin.providers.configDescription') }}</span>
                  </template>
                  <div style="font-size: 12px; line-height: 1.8;">
                    {{ $t('admin.providers.enableLimitTip') }}<br/>
                    {{ $t('admin.providers.noLimitTip') }}
                  </div>
                </el-alert>
                <el-row :gutter="20">
                  <el-col :span="8">
                    <div class="resource-limit-item">
                      <div class="resource-limit-label">
                        <el-icon><Cpu /></el-icon>
                        <span>{{ $t('admin.providers.limitCPU') }}</span>
                      </div>
                      <el-switch
                        v-model="addProviderForm.containerLimitCpu"
                        :active-text="$t('admin.providers.limited')"
                        :inactive-text="$t('admin.providers.unlimited')"
                        inline-prompt
                        style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
                      />
                      <div class="resource-limit-tip">
                        <el-icon size="12"><InfoFilled /></el-icon>
                        <span>{{ $t('admin.providers.defaultNoLimitCPU') }}</span>
                      </div>
                    </div>
                  </el-col>
                  <el-col :span="8">
                    <div class="resource-limit-item">
                      <div class="resource-limit-label">
                        <el-icon><Memo /></el-icon>
                        <span>{{ $t('admin.providers.limitMemory') }}</span>
                      </div>
                      <el-switch
                        v-model="addProviderForm.containerLimitMemory"
                        :active-text="$t('admin.providers.limited')"
                        :inactive-text="$t('admin.providers.unlimited')"
                        inline-prompt
                        style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
                      />
                      <div class="resource-limit-tip">
                        <el-icon size="12"><InfoFilled /></el-icon>
                        <span>{{ $t('admin.providers.defaultNoLimitMemory') }}</span>
                      </div>
                    </div>
                  </el-col>
                  <el-col :span="8">
                    <div class="resource-limit-item">
                      <div class="resource-limit-label">
                        <el-icon><Coin /></el-icon>
                        <span>{{ $t('admin.providers.limitDisk') }}</span>
                      </div>
                      <el-switch
                        v-model="addProviderForm.containerLimitDisk"
                        :active-text="$t('admin.providers.limited')"
                        :inactive-text="$t('admin.providers.unlimited')"
                        inline-prompt
                        style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
                      />
                      <div class="resource-limit-tip">
                        <el-icon size="12"><InfoFilled /></el-icon>
                        <span>{{ $t('admin.providers.defaultLimitDisk') }}</span>
                      </div>
                    </div>
                  </el-col>
                </el-row>
              </el-card>
            </div>

            <!-- 虚拟机资源限制配置 -->
            <div style="margin-top: 20px;">
              <el-card shadow="hover">
                <template #header>
                  <div style="display: flex; align-items: center; justify-content: space-between;">
                    <div style="display: flex; align-items: center; font-weight: 600;">
                      <el-icon size="18" style="margin-right: 8px;"><Monitor /></el-icon>
                      <span>{{ $t('admin.providers.vmResourceLimits') }}</span>
                    </div>
                    <el-tag size="small" type="success">Virtual Machine</el-tag>
                  </div>
                </template>
                <el-alert
                  type="warning"
                  :closable="false"
                  show-icon
                  style="margin-bottom: 20px;"
                >
                  <template #title>
                    <span style="font-size: 13px;">{{ $t('admin.providers.configDescription') }}</span>
                  </template>
                  <div style="font-size: 12px; line-height: 1.8;">
                    {{ $t('admin.providers.enableLimitTip') }}<br/>
                    {{ $t('admin.providers.noLimitTip') }}
                  </div>
                </el-alert>
                <el-row :gutter="20">
                  <el-col :span="8">
                    <div class="resource-limit-item">
                      <div class="resource-limit-label">
                        <el-icon><Cpu /></el-icon>
                        <span>{{ $t('admin.providers.limitCPU') }}</span>
                      </div>
                      <el-switch
                        v-model="addProviderForm.vmLimitCpu"
                        :active-text="$t('admin.providers.limited')"
                        :inactive-text="$t('admin.providers.unlimited')"
                        inline-prompt
                        style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
                      />
                      <div class="resource-limit-tip">
                        <el-icon size="12"><InfoFilled /></el-icon>
                        <span>{{ $t('admin.providers.defaultLimitCPU') }}</span>
                      </div>
                    </div>
                  </el-col>
                  <el-col :span="8">
                    <div class="resource-limit-item">
                      <div class="resource-limit-label">
                        <el-icon><Memo /></el-icon>
                        <span>{{ $t('admin.providers.limitMemory') }}</span>
                      </div>
                      <el-switch
                        v-model="addProviderForm.vmLimitMemory"
                        :active-text="$t('admin.providers.limited')"
                        :inactive-text="$t('admin.providers.unlimited')"
                        inline-prompt
                        style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
                      />
                      <div class="resource-limit-tip">
                        <el-icon size="12"><InfoFilled /></el-icon>
                        <span>{{ $t('admin.providers.defaultLimitMemory') }}</span>
                      </div>
                    </div>
                  </el-col>
                  <el-col :span="8">
                    <div class="resource-limit-item">
                      <div class="resource-limit-label">
                        <el-icon><Coin /></el-icon>
                        <span>{{ $t('admin.providers.limitDisk') }}</span>
                      </div>
                      <el-switch
                        v-model="addProviderForm.vmLimitDisk"
                        :active-text="$t('admin.providers.limited')"
                        :inactive-text="$t('admin.providers.unlimited')"
                        inline-prompt
                        style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
                      />
                      <div class="resource-limit-tip">
                        <el-icon size="12"><InfoFilled /></el-icon>
                        <span>{{ $t('admin.providers.defaultLimitDisk') }}</span>
                      </div>
                    </div>
                  </el-col>
                </el-row>
              </el-card>
            </div>

            <!-- ProxmoxVE存储配置 -->
            <el-form-item
              v-if="addProviderForm.type === 'proxmox'"
              :label="$t('admin.providers.storagePool')"
              prop="storagePool"
            >
              <el-input
                v-model="addProviderForm.storagePool"
                :placeholder="$t('admin.providers.storagePoolPlaceholder')"
                maxlength="64"
                show-word-limit
              >
                <template #prepend>
                  <el-icon><FolderOpened /></el-icon>
                </template>
              </el-input>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.proxmoxStorageTip') }}
                </el-text>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- IP映射配置 -->
        <el-tab-pane
          :label="$t('admin.providers.ipMappingConfig')"
          name="mapping"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-alert
              :title="$t('admin.providers.portMappingConfigTitle')"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              {{ $t('admin.providers.portMappingConfigMessage') }}
            </el-alert>

            <el-form-item
              :label="$t('admin.providers.defaultPortCount')"
              prop="defaultPortCount"
            >
              <el-input-number
                v-model="addProviderForm.defaultPortCount"
                :min="1"
                :max="50"
                :step="1"
                :controls="false"
                placeholder="10"
                style="width: 200px"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.defaultPortCountTip') }}
                </el-text>
              </div>
            </el-form-item>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item
                  :label="$t('admin.providers.portRangeStart')"
                  prop="portRangeStart"
                >
                  <el-input-number
                    v-model="addProviderForm.portRangeStart"
                    :min="1024"
                    :max="65535"
                    :step="1"
                    :controls="false"
                    placeholder="10000"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      {{ $t('admin.providers.portRangeStartTip') }}
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  :label="$t('admin.providers.portRangeEnd')"
                  prop="portRangeEnd"
                >
                  <el-input-number
                    v-model="addProviderForm.portRangeEnd"
                    :min="1024"
                    :max="65535"
                    :step="1"
                    :controls="false"
                    placeholder="65535"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      {{ $t('admin.providers.portRangeEndTip') }}
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item
              :label="$t('admin.providers.networkType')"
              prop="networkType"
            >
              <el-select
                v-model="addProviderForm.networkType"
                :placeholder="$t('admin.providers.networkTypePlaceholder')"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.providers.natIPv4')"
                  value="nat_ipv4"
                />
                <el-option
                  :label="$t('admin.providers.natIPv4IPv6')"
                  value="nat_ipv4_ipv6"
                />
                <el-option
                  :label="$t('admin.providers.dedicatedIPv4')"
                  value="dedicated_ipv4"
                />
                <el-option
                  :label="$t('admin.providers.dedicatedIPv4IPv6')"
                  value="dedicated_ipv4_ipv6"
                />
                <el-option
                  :label="$t('admin.providers.ipv6Only')"
                  value="ipv6_only"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.networkTypeTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- Docker 端口映射方式（固定为 native，不可选择） -->
            <el-form-item
              v-if="addProviderForm.type === 'docker'"
              :label="$t('admin.providers.portMappingMethod')"
            >
              <el-input
                value="Native（原生）"
                disabled
                style="width: 100%"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.dockerNativeMappingTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- IPv4端口映射方式 -->
            <el-form-item
              v-if="(addProviderForm.type === 'lxd' || addProviderForm.type === 'incus') && addProviderForm.networkType !== 'ipv6_only'"
              :label="$t('admin.providers.ipv4PortMappingMethod')"
              prop="ipv4PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv4PortMappingMethod"
                :placeholder="$t('admin.providers.ipv4PortMappingMethodPlaceholder')"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.providers.deviceProxyRecommended')"
                  value="device_proxy"
                />
                <el-option
                  label="Iptables"
                  value="iptables"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.ipv4PortMappingMethodTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- IPv6端口映射方式 -->
            <el-form-item
              v-if="(addProviderForm.type === 'lxd' || addProviderForm.type === 'incus') && (addProviderForm.networkType === 'nat_ipv4_ipv6' || addProviderForm.networkType === 'dedicated_ipv4_ipv6' || addProviderForm.networkType === 'ipv6_only')"
              :label="$t('admin.providers.ipv6PortMappingMethod')"
              prop="ipv6PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv6PortMappingMethod"
                :placeholder="$t('admin.providers.ipv6PortMappingMethodPlaceholder')"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.providers.deviceProxyRecommended')"
                  value="device_proxy"
                />
                <el-option
                  label="Iptables"
                  value="iptables"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.ipv6PortMappingMethodTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- Proxmox IPv4端口映射方式 -->
            <el-form-item
              v-if="addProviderForm.type === 'proxmox' && addProviderForm.networkType !== 'ipv6_only'"
              :label="$t('admin.providers.ipv4PortMappingMethod')"
              prop="ipv4PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv4PortMappingMethod"
                :placeholder="$t('admin.providers.ipv4PortMappingMethodPlaceholder')"
                style="width: 100%"
              >
                <el-option
                  v-if="addProviderForm.networkType === 'dedicated_ipv4' || addProviderForm.networkType === 'dedicated_ipv4_ipv6'"
                  :label="$t('admin.providers.nativeRecommended')"
                  value="native"
                />
                <el-option
                  label="Iptables"
                  value="iptables"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.proxmoxIPv4MappingTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- Proxmox IPv6端口映射方式 -->
            <el-form-item
              v-if="addProviderForm.type === 'proxmox' && (addProviderForm.networkType === 'nat_ipv4_ipv6' || addProviderForm.networkType === 'dedicated_ipv4_ipv6' || addProviderForm.networkType === 'ipv6_only')"
              :label="$t('admin.providers.ipv6PortMappingMethod')"
              prop="ipv6PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv6PortMappingMethod"
                :placeholder="$t('admin.providers.ipv6PortMappingMethodPlaceholder')"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.providers.nativeRecommended')"
                  value="native"
                />
                <el-option
                  label="Iptables"
                  value="iptables"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.proxmoxIPv6MappingTip') }}
                </el-text>
              </div>
            </el-form-item>

            <el-alert
              :title="$t('admin.providers.mappingTypeDescription')"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 20px;"
            >
              <ul style="margin: 0; padding-left: 20px;">
                <li><strong>{{ $t('admin.providers.natMapping') }}:</strong> {{ $t('admin.providers.natMappingDesc') }}</li>
                <li><strong>{{ $t('admin.providers.dedicatedMapping') }}:</strong> {{ $t('admin.providers.dedicatedMappingDesc') }}</li>
                <li><strong>{{ $t('admin.providers.ipv6Support') }}:</strong> {{ $t('admin.providers.ipv6SupportDesc') }}</li>
                <li><strong>Docker:</strong> {{ $t('admin.providers.dockerMappingDesc') }}</li>
                <li><strong>LXD/Incus:</strong> {{ $t('admin.providers.lxdIncusMappingDesc') }}</li>
                <li><strong>Proxmox VE:</strong> {{ $t('admin.providers.proxmoxMappingDesc') }}</li>
              </ul>
            </el-alert>
          </el-form>
        </el-tab-pane>

        <!-- 带宽配置 -->
        <el-tab-pane
          :label="$t('admin.providers.bandwidthConfig')"
          name="bandwidth"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-alert
              :title="$t('admin.providers.bandwidthConfigTitle')"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              {{ $t('admin.providers.bandwidthConfigDesc') }}
            </el-alert>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item
                  :label="$t('admin.providers.defaultInboundBandwidth')"
                  prop="defaultInboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.defaultInboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    :controls="false"
                    placeholder="300"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      {{ $t('admin.providers.defaultInboundBandwidthTip') }}
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  :label="$t('admin.providers.defaultOutboundBandwidth')"
                  prop="defaultOutboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.defaultOutboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    :controls="false"
                    placeholder="300"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      {{ $t('admin.providers.defaultOutboundBandwidthTip') }}
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item
                  :label="$t('admin.providers.maxInboundBandwidth')"
                  prop="maxInboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.maxInboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    :controls="false"
                    placeholder="1000"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      {{ $t('admin.providers.maxInboundBandwidthTip') }}
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  :label="$t('admin.providers.maxOutboundBandwidth')"
                  prop="maxOutboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.maxOutboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    :controls="false"
                    placeholder="1000"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      {{ $t('admin.providers.maxOutboundBandwidthTip') }}
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.trafficConfig') }}</span>
            </el-divider>

            <el-form-item
              :label="$t('admin.providers.maxTraffic')"
              prop="maxTraffic"
            >
              <el-input-number
                v-model="maxTrafficTB"
                :min="0.001"
                :max="10"
                :step="0.1"
                :precision="3"
                :controls="false"
                placeholder="1"
                style="width: 100%"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.maxTrafficTip') }}
                </el-text>
              </div>
            </el-form-item>

            <el-form-item
              :label="$t('admin.providers.trafficCountMode')"
              prop="trafficCountMode"
            >
              <el-select
                v-model="addProviderForm.trafficCountMode"
                :placeholder="$t('admin.providers.selectTrafficCountMode')"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.providers.trafficCountModeBoth')"
                  value="both"
                />
                <el-option
                  :label="$t('admin.providers.trafficCountModeOut')"
                  value="out"
                />
                <el-option
                  :label="$t('admin.providers.trafficCountModeIn')"
                  value="in"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.trafficCountModeTip') }}
                </el-text>
              </div>
            </el-form-item>

            <el-form-item
              :label="$t('admin.providers.trafficMultiplier')"
              prop="trafficMultiplier"
            >
              <el-input-number
                v-model="addProviderForm.trafficMultiplier"
                :min="0.1"
                :max="10"
                :step="0.1"
                :precision="2"
                :controls="false"
                placeholder="1.0"
                style="width: 100%"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.trafficMultiplierTip') }}
                </el-text>
              </div>
            </el-form-item>

            <el-alert
              :title="$t('admin.providers.bandwidthMechanismTitle')"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 20px;"
            >
              <ul style="margin: 0; padding-left: 20px;">
                <li><strong>{{ $t('admin.providers.defaultBandwidth') }}:</strong> {{ $t('admin.providers.defaultBandwidthDesc') }}</li>
                <li><strong>{{ $t('admin.providers.maxBandwidth') }}:</strong> {{ $t('admin.providers.maxBandwidthDesc') }}</li>
                <li><strong>{{ $t('admin.providers.userLevel') }}:</strong> {{ $t('admin.providers.userLevelDesc') }}</li>
                <li><strong>{{ $t('admin.providers.trafficLimit') }}:</strong> {{ $t('admin.providers.trafficLimitDesc') }}</li>
              </ul>
            </el-alert>
          </el-form>
        </el-tab-pane>

        <!-- 等级限制配置 -->
        <el-tab-pane
          :label="$t('admin.providers.levelLimits')"
          name="levelLimits"
        >
          <div class="level-limits-container">
            <div style="margin-bottom: 12px; display: flex; justify-content: space-between; align-items: center;">
              <el-text
                type="info"
                size="small"
              >
                {{ $t('admin.providers.levelLimitsTip') }}
              </el-text>
              <el-button
                type="primary"
                size="small"
                @click="resetLevelLimitsToDefault"
              >
                {{ $t('admin.providers.resetToDefault') }}
              </el-button>
            </div>

            <!-- 等级配置循环 -->
            <div
              v-for="level in 5"
              :key="level"
              class="level-config-card"
            >
              <div class="level-header">
                <el-tag
                  :type="getLevelTagType(level)"
                  size="large"
                >
                  {{ $t('admin.providers.level') }} {{ level }}
                </el-tag>
              </div>

              <el-form
                :model="addProviderForm.levelLimits[level]"
                label-width="120px"
                class="level-form"
              >
                <el-row :gutter="20">
                  <el-col :span="12">
                    <el-form-item :label="$t('admin.providers.maxInstances')">
                      <el-input-number
                        v-model="addProviderForm.levelLimits[level].maxInstances"
                        :min="0"
                        :max="100"
                        :controls="false"
                        style="width: 100%;"
                      />
                    </el-form-item>
                  </el-col>
                  <el-col :span="12">
                    <el-form-item :label="$t('admin.providers.maxTrafficMB')">
                      <el-input-number
                        v-model="addProviderForm.levelLimits[level].maxTraffic"
                        :min="0"
                        :max="10240000"
                        :step="1024"
                        :controls="false"
                        style="width: 100%;"
                      />
                    </el-form-item>
                  </el-col>
                </el-row>

                <el-row :gutter="20">
                  <el-col :span="12">
                    <el-form-item :label="$t('admin.providers.maxCPU')">
                      <el-input-number
                        v-model="addProviderForm.levelLimits[level].maxResources.cpu"
                        :min="1"
                        :max="128"
                        :controls="false"
                        style="width: 100%;"
                      />
                    </el-form-item>
                  </el-col>
                  <el-col :span="12">
                    <el-form-item :label="$t('admin.providers.maxMemoryMB')">
                      <el-input-number
                        v-model="addProviderForm.levelLimits[level].maxResources.memory"
                        :min="128"
                        :max="131072"
                        :step="128"
                        :controls="false"
                        style="width: 100%;"
                      />
                    </el-form-item>
                  </el-col>
                </el-row>

                <el-row :gutter="20">
                  <el-col :span="12">
                    <el-form-item :label="$t('admin.providers.maxDiskMB')">
                      <el-input-number
                        v-model="addProviderForm.levelLimits[level].maxResources.disk"
                        :min="1024"
                        :max="1048576"
                        :step="1024"
                        :controls="false"
                        style="width: 100%;"
                      />
                    </el-form-item>
                  </el-col>
                  <el-col :span="12">
                    <el-form-item :label="$t('admin.providers.maxBandwidthMbps')">
                      <el-input-number
                        v-model="addProviderForm.levelLimits[level].maxResources.bandwidth"
                        :min="10"
                        :max="10000"
                        :step="10"
                        :controls="false"
                        style="width: 100%;"
                      />
                    </el-form-item>
                  </el-col>
                </el-row>
              </el-form>
            </div>
          </div>
        </el-tab-pane>

        <!-- 高级设置 -->
        <el-tab-pane
          :label="$t('admin.providers.advancedSettings')"
          name="advanced"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-form-item
              :label="$t('admin.providers.expiresAt')"
              prop="expiresAt"
            >
              <el-date-picker
                v-model="addProviderForm.expiresAt"
                type="datetime"
                :placeholder="$t('admin.providers.expiresAtPlaceholder')"
                format="YYYY-MM-DD HH:mm:ss"
                value-format="YYYY-MM-DD HH:mm:ss"
                :disabled-date="(time) => time.getTime() < Date.now() - 8.64e7"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.expiresAtTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- 并发控制设置 -->
            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.concurrencyControl') }}</span>
            </el-divider>
            
            <el-form-item
              :label="$t('admin.providers.allowConcurrentTasks')"
              prop="allowConcurrentTasks"
            >
              <el-switch
                v-model="addProviderForm.allowConcurrentTasks"
                :active-text="$t('common.yes')"
                :inactive-text="$t('common.no')"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.allowConcurrentTasksTip') }}
                </el-text>
              </div>
            </el-form-item>

            <el-form-item
              v-if="addProviderForm.allowConcurrentTasks"
              :label="$t('admin.providers.maxConcurrentTasks')"
              prop="maxConcurrentTasks"
            >
              <el-input-number
                v-model="addProviderForm.maxConcurrentTasks"
                :min="1"
                :max="10"
                :step="1"
                :controls="false"
                placeholder="1"
                style="width: 200px"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.maxConcurrentTasksTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- 任务轮询设置 -->
            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.taskPollingSettings') }}</span>
            </el-divider>
            
            <el-form-item
              :label="$t('admin.providers.enableTaskPolling')"
              prop="enableTaskPolling"
            >
              <el-switch
                v-model="addProviderForm.enableTaskPolling"
                :active-text="$t('common.yes')"
                :inactive-text="$t('common.no')"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.enableTaskPollingTip') }}
                </el-text>
              </div>
            </el-form-item>

            <el-form-item
              v-if="addProviderForm.enableTaskPolling"
              :label="$t('admin.providers.taskPollInterval')"
              prop="taskPollInterval"
            >
              <el-input-number
                v-model="addProviderForm.taskPollInterval"
                :min="5"
                :max="300"
                :step="5"
                :controls="false"
                placeholder="60"
                style="width: 200px"
              />
              <span style="margin-left: 10px; color: #666;">{{ $t('common.seconds') }}</span>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.taskPollIntervalTip') }}
                </el-text>
              </div>
            </el-form-item>

            <!-- 操作执行规则设置 -->
            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.executionRules') }}</span>
            </el-divider>
            
            <el-form-item
              :label="$t('admin.providers.executionRule')"
              prop="executionRule"
            >
              <el-select
                v-model="addProviderForm.executionRule"
                :placeholder="$t('admin.providers.executionRulePlaceholder')"
                style="width: 200px"
              >
                <el-option
                  :label="$t('admin.providers.executionRuleAuto')"
                  value="auto"
                >
                  <span>{{ $t('admin.providers.executionRuleAuto') }}</span>
                  <span style="float: right; color: #8492a6; font-size: 12px;">{{ $t('admin.providers.executionRuleAutoTip') }}</span>
                </el-option>
                <el-option
                  :label="$t('admin.providers.executionRuleAPIOnly')"
                  value="api_only"
                >
                  <span>{{ $t('admin.providers.executionRuleAPIOnly') }}</span>
                  <span style="float: right; color: #8492a6; font-size: 12px;">{{ $t('admin.providers.executionRuleAPIOnlyTip') }}</span>
                </el-option>
                <el-option
                  :label="$t('admin.providers.executionRuleSSHOnly')"
                  value="ssh_only"
                >
                  <span>{{ $t('admin.providers.executionRuleSSHOnly') }}</span>
                  <span style="float: right; color: #8492a6; font-size: 12px;">{{ $t('admin.providers.executionRuleSSHOnlyTip') }}</span>
                </el-option>
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.executionRuleTip') }}
                </el-text>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>
      
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelAddServer">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="addProviderLoading"
            @click="submitAddServer"
          >{{ $t('common.save') }}</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 自动配置结果对话框 -->
    <el-dialog 
      v-model="configDialog.visible" 
      :title="$t('admin.providers.autoConfigAPI')" 
      width="900px"
      :close-on-click-modal="false"
      :close-on-press-escape="false"
    >
      <div v-if="configDialog.provider">
        <!-- 历史记录视图 -->
        <div v-if="configDialog.showHistory">
          <el-alert
            :title="$t('admin.providers.configHistory', { type: configDialog.provider.type.toUpperCase() })"
            type="info"
            :closable="false"
            show-icon
            style="margin-bottom: 20px;"
          >
            <template #default>
              <p>{{ $t('admin.providers.configHistoryMessage') }}</p>
            </template>
          </el-alert>

          <!-- 正在运行的任务 -->
          <div
            v-if="configDialog.runningTask"
            style="margin-bottom: 20px;"
          >
            <el-alert
              :title="$t('admin.providers.runningConfigTask')"
              type="warning"
              :closable="false"
              show-icon
            >
              <template #default>
                <p>{{ $t('admin.providers.taskID') }}: {{ configDialog.runningTask.id }}</p>
                <p>{{ $t('admin.providers.startTime') }}: {{ new Date(configDialog.runningTask.startedAt).toLocaleString() }}</p>
                <p>{{ $t('admin.providers.executor') }}: {{ configDialog.runningTask.executorName }}</p>
              </template>
            </el-alert>
          </div>

          <!-- 历史任务列表 -->
          <div v-if="configDialog.historyTasks.length > 0">
            <h4>{{ $t('admin.providers.configHistoryRecords') }}</h4>
            <el-table
              :data="configDialog.historyTasks"
              size="small"
              style="margin-bottom: 20px;"
            >
              <el-table-column
                prop="id"
                :label="$t('admin.providers.taskID')"
                width="70"
              />
              <el-table-column
                :label="$t('admin.providers.status')"
                width="80"
              >
                <template #default="{ row }">
                  <el-tag 
                    :type="getTaskStatusType(row.status)"
                    size="small"
                  >
                    {{ getTaskStatusText(row.status) }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                :label="$t('admin.providers.executionTime')"
                width="140"
              >
                <template #default="{ row }">
                  {{ new Date(row.createdAt).toLocaleString() }}
                </template>
              </el-table-column>
              <el-table-column
                prop="executorName"
                :label="$t('admin.providers.executor')"
                width="80"
              />
              <el-table-column
                prop="duration"
                :label="$t('admin.providers.duration')"
                width="70"
              />
              <el-table-column
                :label="$t('admin.providers.result')"
                show-overflow-tooltip
              >
                <template #default="{ row }">
                  <span
                    v-if="row.success"
                    style="color: #67C23A;"
                  >✅ {{ $t('common.success') }}</span>
                  <span
                    v-else-if="row.status === 'failed'"
                    style="color: #F56C6C;"
                  >❌ {{ row.errorMessage || $t('common.failed') }}</span>
                  <span v-else>{{ row.logSummary || '-' }}</span>
                </template>
              </el-table-column>
              <el-table-column
                :label="$t('common.actions')"
                width="100"
              >
                <template #default="{ row }">
                  <el-button 
                    type="primary" 
                    size="small"
                    @click="viewTaskLog(row.id)"
                  >
                    {{ $t('admin.providers.viewLog') }}
                  </el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>

          <!-- 操作按钮 -->
          <div style="text-align: center; margin-top: 20px;">
            <el-button 
              v-if="configDialog.runningTask"
              type="primary"
              @click="viewRunningTask"
            >
              {{ $t('admin.providers.viewRunningTaskLog') }}
            </el-button>
            <el-button 
              type="warning"
              @click="rerunConfiguration"
            >
              {{ $t('admin.providers.rerunConfig') }}
            </el-button>
            <el-button @click="configDialog.visible = false">
              {{ $t('common.close') }}
            </el-button>
          </div>
        </div>
      </div>
    </el-dialog>

    <!-- 任务日志查看对话框 -->
    <el-dialog
      v-model="taskLogDialog.visible"
      :title="$t('admin.providers.taskLog')"
      width="80%"
      style="max-width: 1000px;"
      :close-on-click-modal="false"
    >
      <div
        v-if="taskLogDialog.loading"
        style="text-align: center; padding: 40px;"
      >
        <el-icon
          class="is-loading"
          style="font-size: 32px;"
        >
          <loading />
        </el-icon>
        <p style="margin-top: 16px;">
          {{ $t('admin.providers.loadingTaskLog') }}
        </p>
      </div>
      <div
        v-else-if="taskLogDialog.error"
        style="text-align: center; padding: 40px;"
      >
        <el-alert 
          type="error" 
          :title="taskLogDialog.error" 
          show-icon 
          :closable="false"
        />
      </div>
      <div v-else>
        <!-- 任务基本信息 -->
        <el-card
          v-if="taskLogDialog.task"
          style="margin-bottom: 20px;"
        >
          <template #header>
            <span>{{ $t('admin.providers.taskInfo') }}</span>
          </template>
          <el-descriptions
            :column="2"
            border
          >
            <el-descriptions-item :label="$t('admin.providers.taskID')">
              {{ taskLogDialog.task.id }}
            </el-descriptions-item>
            <el-descriptions-item label="Provider">
              {{ taskLogDialog.task.providerName }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.providers.taskType')">
              {{ taskLogDialog.task.taskType }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.providers.status')">
              <el-tag :type="getTaskStatusType(taskLogDialog.task.status)">
                {{ getTaskStatusText(taskLogDialog.task.status) }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.providers.executor')">
              {{ taskLogDialog.task.executorName }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.providers.duration')">
              {{ taskLogDialog.task.duration }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.providers.startTime')">
              {{ taskLogDialog.task.startedAt ? new Date(taskLogDialog.task.startedAt).toLocaleString() : '-' }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.providers.completionTime')">
              {{ taskLogDialog.task.completedAt ? new Date(taskLogDialog.task.completedAt).toLocaleString() : '-' }}
            </el-descriptions-item>
          </el-descriptions>
          <div
            v-if="taskLogDialog.task.errorMessage"
            style="margin-top: 16px;"
          >
            <el-alert 
              type="error" 
              :title="taskLogDialog.task.errorMessage" 
              show-icon 
              :closable="false"
            />
          </div>
        </el-card>

        <!-- 日志内容 -->
        <el-card>
          <template #header>
            <div style="display: flex; justify-content: space-between; align-items: center;">
              <span>{{ $t('admin.providers.executionLog') }}</span>
              <el-button 
                v-if="taskLogDialog.task && taskLogDialog.task.logOutput" 
                size="small"
                @click="copyTaskLog"
              >
                {{ $t('admin.providers.copyLog') }}
              </el-button>
            </div>
          </template>
          <div 
            class="task-log-content"
            :style="{
              height: '400px',
              overflow: 'auto',
              backgroundColor: '#1e1e1e',
              color: '#ffffff',
              padding: '16px',
              fontFamily: 'Monaco, Consolas, monospace',
              fontSize: '13px',
              lineHeight: '1.5',
              borderRadius: '4px'
            }"
          >
            <pre v-if="taskLogDialog.task && taskLogDialog.task.logOutput">{{ taskLogDialog.task.logOutput }}</pre>
            <div
              v-else
              style="color: #999; text-align: center; padding: 40px;"
            >
              暂无日志内容
            </div>
          </div>
        </el-card>
      </div>

      <template #footer>
        <div style="text-align: center;">
          <el-button @click="taskLogDialog.visible = false">
            关闭
          </el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import { InfoFilled, DocumentCopy, Loading, Cpu, Monitor, FolderOpened, Box, Memo, Coin, Search } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { getProviderList, createProvider, updateProvider, deleteProvider, freezeProvider, unfreezeProvider, checkProviderHealth, autoConfigureProvider, getConfigurationTaskDetail, testSSHConnection as testSSHConnectionAPI } from '@/api/admin'
import { countries, getFlagEmoji, getCountryByName, getCountriesByRegion } from '@/utils/countries'
import { formatMemorySize, formatDiskSize } from '@/utils/unit-formatter'
import { useUserStore } from '@/pinia/modules/user'

const { t } = useI18n()

const providers = ref([])
const loading = ref(false)
const showAddDialog = ref(false)
const addProviderLoading = ref(false)
const addProviderFormRef = ref()
const isEditing = ref(false)
const activeConfigTab = ref('basic') // 标签页状态

// 搜索表单
const searchForm = reactive({
  name: '',
  type: '',
  status: ''
})

// 分页
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 服务器表单
const addProviderForm = reactive({
  id: null,
  name: '',
  type: '',
  host: '',
  portIP: '', // 端口映射使用的公网IP
  port: 22,
  username: '',
  password: '',
  sshKey: '',
  authMethod: 'password', // 认证方式：'password' 或 'sshKey'
  description: '',
  region: '',
  country: '',
  countryCode: '',
  city: '',
  containerEnabled: true,
  vmEnabled: false,
  architecture: 'amd64', // 架构字段，默认amd64
  status: 'active',
  expiresAt: '', // 过期时间
  maxContainerInstances: 0, // 最大容器数，0表示无限制
  maxVMInstances: 0, // 最大虚拟机数，0表示无限制
  allowConcurrentTasks: false, // 是否允许并发任务，默认false
  maxConcurrentTasks: 1, // 最大并发任务数，默认1
  taskPollInterval: 60, // 任务轮询间隔（秒），默认60秒
  enableTaskPolling: true, // 是否启用任务轮询，默认true
  // 存储配置（ProxmoxVE专用）
  storagePool: 'local', // 存储池名称，默认local
  // 端口映射配置
  defaultPortCount: 10, // 每个实例默认端口数量
  portRangeStart: 10000, // 端口范围起始
  portRangeEnd: 65535, // 端口范围结束
  networkType: 'nat_ipv4', // 网络配置类型，默认NAT IPv4
  // 带宽配置
  defaultInboundBandwidth: 300, // 默认入站带宽限制（Mbps）
  defaultOutboundBandwidth: 300, // 默认出站带宽限制（Mbps）
  maxInboundBandwidth: 1000, // 最大入站带宽限制（Mbps）
  maxOutboundBandwidth: 1000, // 最大出站带宽限制（Mbps）
  // 流量配置
  maxTraffic: 1048576, // 最大流量限制（MB），默认1TB
  trafficCountMode: 'both', // 流量统计模式：both(双向), out(仅出向), in(仅入向)
  trafficMultiplier: 1.0, // 流量计费倍率，默认1.0
  ipv4PortMappingMethod: 'device_proxy', // IPv4端口映射方式：device_proxy, iptables, native
  ipv6PortMappingMethod: 'device_proxy',  // IPv6端口映射方式：device_proxy, iptables, native
  executionRule: 'auto', // 操作轮转规则：auto(自动切换), api_only(仅API), ssh_only(仅SSH)
  sshConnectTimeout: 30, // SSH连接超时（秒），默认30秒
  sshExecuteTimeout: 300, // SSH执行超时（秒），默认300秒
  // 容器资源限制配置
  containerLimitCpu: false, // 容器是否限制CPU，默认不限制
  containerLimitMemory: false, // 容器是否限制内存，默认不限制
  containerLimitDisk: true, // 容器是否限制硬盘，默认限制
  // 虚拟机资源限制配置
  vmLimitCpu: true, // 虚拟机是否限制CPU，默认限制
  vmLimitMemory: true, // 虚拟机是否限制内存，默认限制
  vmLimitDisk: true, // 虚拟机是否限制硬盘，默认限制
  // 节点级别等级限制配置
  levelLimits: {
    1: { maxInstances: 1, maxResources: { cpu: 1, memory: 350, disk: 1025, bandwidth: 100 }, maxTraffic: 102400 },
    2: { maxInstances: 2, maxResources: { cpu: 2, memory: 512, disk: 2048, bandwidth: 200 }, maxTraffic: 102400 },
    3: { maxInstances: 3, maxResources: { cpu: 3, memory: 1024, disk: 4096, bandwidth: 500 }, maxTraffic: 204800 },
    4: { maxInstances: 4, maxResources: { cpu: 4, memory: 4096, disk: 8192, bandwidth: 1000 }, maxTraffic: 409600 },
    5: { maxInstances: 5, maxResources: { cpu: 5, memory: 8192, disk: 16384, bandwidth: 2000 }, maxTraffic: 512000 }
  }
})

// 流量单位转换：TB 转 MB (1TB = 1024 * 1024 MB = 1048576 MB)
const TB_TO_MB = 1048576

// 计算属性：maxTraffic 的 TB 单位显示
const maxTrafficTB = computed({
  get: () => {
    // 从 MB 转换为 TB
    return Number((addProviderForm.maxTraffic / TB_TO_MB).toFixed(3))
  },
  set: (value) => {
    // 从 TB 转换为 MB
    addProviderForm.maxTraffic = Math.round(value * TB_TO_MB)
  }
})

// 获取等级标签类型
const getLevelTagType = (level) => {
  const types = {
    1: 'info',
    2: 'success',
    3: 'warning',
    4: 'danger',
    5: 'primary'
  }
  return types[level] || 'info'
}

// 恢复默认等级限制
const resetLevelLimitsToDefault = () => {
  ElMessageBox.confirm(
    '确定要恢复所有等级的默认限制值吗？',
    '确认操作',
    {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(() => {
    // 恢复默认值
    addProviderForm.levelLimits = {
      1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400 },
      2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800 },
      3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200 },
      4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600 },
      5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000 }
    }
    ElMessage.success(t('admin.providers.levelLimitsRestored'))
  }).catch(() => {
    // 用户取消操作
  })
}

// 解析等级限制配置
const parseLevelLimits = (levelLimitsStr) => {
  // 默认等级限制配置
  const defaultLevelLimits = {
    1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400 },
    2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800 },
    3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200 },
    4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600 },
    5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000 }
  }

  if (!levelLimitsStr) {
    return defaultLevelLimits
  }

  try {
    const parsed = typeof levelLimitsStr === 'string' ? JSON.parse(levelLimitsStr) : levelLimitsStr
    // 确保所有等级都有配置
    for (let i = 1; i <= 5; i++) {
      if (!parsed[i]) {
        parsed[i] = defaultLevelLimits[i]
      }
    }
    return parsed
  } catch (e) {
    console.error('解析等级限制配置失败:', e)
    return defaultLevelLimits
  }
}

// 表单验证规则
const addProviderRules = {
  name: [
    { required: true, message: () => t('admin.providers.validation.serverNameRequired'), trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9]+$/, message: () => t('admin.providers.validation.serverNamePattern'), trigger: 'blur' },
    { max: 7, message: () => t('admin.providers.validation.serverNameMaxLength'), trigger: 'blur' }
  ],
  type: [
    { required: true, message: () => t('admin.providers.validation.serverTypeRequired'), trigger: 'change' }
  ],
  host: [
    { required: true, message: () => t('admin.providers.validation.hostRequired'), trigger: 'blur' }
  ],
  port: [
    { required: true, message: () => t('admin.providers.validation.portRequired'), trigger: 'blur' }
  ],
  username: [
    { required: true, message: () => t('admin.providers.validation.usernameRequired'), trigger: 'blur' }
  ],
  architecture: [
    { required: true, message: () => t('admin.providers.validation.architectureRequired'), trigger: 'change' }
  ],
  status: [
    { required: true, message: () => t('admin.providers.validation.statusRequired'), trigger: 'change' }
  ]
}

// 验证至少选择一种虚拟化类型
const validateVirtualizationType = () => {
  if (!addProviderForm.containerEnabled && !addProviderForm.vmEnabled) {
    ElMessage.warning(t('admin.providers.selectVirtualizationType'))
    return false
  }
  return true
}

// 国家列表数据
const countriesData = ref(countries)
const groupedCountries = ref(getCountriesByRegion())

// 获取国旗 emoji (使用工具函数)
// const getFlagEmoji 已从 utils/countries 导入

// 格式化位置信息
const formatLocation = (provider) => {
  const parts = []
  if (provider.city) {
    parts.push(provider.city)
  }
  if (provider.country) {
    parts.push(provider.country)
  } else if (provider.region) {
    parts.push(provider.region)
  }
  return parts.length > 0 ? parts.join(', ') : '-'
}

// 国家选择变化处理
const onCountryChange = (countryName) => {
  const country = getCountryByName(countryName)
  if (country) {
    addProviderForm.countryCode = country.code
    // 如果地区为空，自动填入国家所属地区
    if (!addProviderForm.region) {
      addProviderForm.region = country.region
    }
  }
}

const loadProviders = async () => {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      pageSize: pageSize.value
    }
    
    // 添加搜索参数
    if (searchForm.name) {
      params.name = searchForm.name
    }
    if (searchForm.type) {
      params.type = searchForm.type
    }
    if (searchForm.status) {
      params.status = searchForm.status
    }
    
    const response = await getProviderList(params)
    providers.value = response.data.list || []
    total.value = response.data.total || 0
  } catch (error) {
    ElMessage.error(t('admin.providers.loadProvidersFailed'))
  } finally {
    loading.value = false
  }
}

// 搜索处理
const handleSearch = () => {
  currentPage.value = 1
  loadProviders()
}

// 重置搜索
const handleReset = () => {
  searchForm.name = ''
  searchForm.type = ''
  searchForm.status = ''
  currentPage.value = 1
  loadProviders()
}

// SSH连接测试相关
const testingConnection = ref(false)
const connectionTestResult = ref(null)

// 测试SSH连接
const testSSHConnection = async () => {
  // 根据认证方式进行验证
  if (!addProviderForm.host || !addProviderForm.username) {
    ElMessage.warning(t('admin.providers.fillHostUserPassword'))
    return
  }

  if (addProviderForm.authMethod === 'password' && !addProviderForm.password) {
    ElMessage.warning(t('admin.providers.fillHostUserPassword'))
    return
  }

  if (addProviderForm.authMethod === 'sshKey' && !addProviderForm.sshKey) {
    ElMessage.warning('请填写SSH密钥')
    return
  }

  testingConnection.value = true
  connectionTestResult.value = null

  try {
    // 根据认证方式构建请求数据
    const requestData = {
      host: addProviderForm.host,
      port: addProviderForm.port || 22,
      username: addProviderForm.username,
      testCount: 3
    }

    // 添加对应的认证信息
    if (addProviderForm.authMethod === 'password') {
      requestData.password = addProviderForm.password
    } else if (addProviderForm.authMethod === 'sshKey') {
      requestData.sshKey = addProviderForm.sshKey
    }

    const result = await testSSHConnectionAPI(requestData)

    if (result.code === 200 && result.data.success) {
      connectionTestResult.value = {
        success: true,
        title: 'SSH连接测试成功',
        type: 'success',
        minLatency: result.data.minLatency,
        maxLatency: result.data.maxLatency,
        avgLatency: result.data.avgLatency,
        recommendedTimeout: result.data.recommendedTimeout
      }
      ElMessage.success('SSH连接测试成功')
    } else {
      connectionTestResult.value = {
        success: false,
        title: 'SSH连接测试失败',
        type: 'error',
        error: result.data.errorMessage || result.msg || '连接失败'
      }
      ElMessage.error('SSH连接测试失败: ' + (result.data.errorMessage || result.msg))
    }
  } catch (error) {
    connectionTestResult.value = {
      success: false,
      title: 'SSH连接测试失败',
      type: 'error',
      error: error.message || '网络请求失败'
    }
    ElMessage.error(t('admin.providers.testFailed') + ': ' + error.message)
  } finally {
    testingConnection.value = false
  }
}

// 应用推荐的超时值
const applyRecommendedTimeout = () => {
  if (connectionTestResult.value && connectionTestResult.value.success) {
    addProviderForm.sshConnectTimeout = connectionTestResult.value.recommendedTimeout
    addProviderForm.sshExecuteTimeout = Math.max(300, connectionTestResult.value.recommendedTimeout * 10)
    ElMessage.success(t('admin.providers.timeoutApplied'))
  }
}

// 认证方式切换处理
const handleAuthMethodChange = (newMethod) => {
  // 切换认证方式时，清空被隐藏的字段
  if (newMethod === 'password') {
    addProviderForm.sshKey = ''
  } else if (newMethod === 'sshKey') {
    addProviderForm.password = ''
  }
}

const cancelAddServer = () => {
  showAddDialog.value = false
  isEditing.value = false
  activeConfigTab.value = 'basic' // 重置标签页状态
  addProviderFormRef.value?.resetFields()
  Object.assign(addProviderForm, {
    id: null,
    name: '',
    type: '',
    host: '',
    portIP: '', // 重置端口IP
    port: 22,
    username: '',
    password: '',
    sshKey: '',
    authMethod: 'password', // 重置认证方式
    description: '',
    region: '',
    country: '',
    countryCode: '',
    city: '',
    containerEnabled: true,
    vmEnabled: false,
    architecture: 'amd64', // 重置架构字段
    status: 'active',
    expiresAt: '', // 重置过期时间
    maxContainerInstances: 0, // 重置最大容器数
    maxVMInstances: 0, // 重置最大虚拟机数
    allowConcurrentTasks: false, // 重置并发任务设置
    maxConcurrentTasks: 1, // 重置最大并发任务数
    taskPollInterval: 60, // 重置任务轮询间隔
    enableTaskPolling: true, // 重置任务轮询开关
    storagePool: 'local', // 重置存储池配置
    // 重置端口映射配置
    defaultPortCount: 10,
    portRangeStart: 10000,
    portRangeEnd: 65535,
    networkType: 'nat_ipv4', // 网络配置类型
    // 重置带宽配置
    defaultInboundBandwidth: 300,
    defaultOutboundBandwidth: 300,
    maxInboundBandwidth: 1000,
    maxOutboundBandwidth: 1000,
    // 重置流量配置 (1TB = 1048576 MB)
    maxTraffic: 1048576,
    trafficCountMode: 'both', // 重置流量统计模式
    trafficMultiplier: 1.0, // 重置流量倍率
    executionRule: 'auto', // 重置操作执行规则
    ipv4PortMappingMethod: 'device_proxy',
    ipv6PortMappingMethod: 'device_proxy',
    // 重置SSH超时配置
    sshConnectTimeout: 30,
    sshExecuteTimeout: 300,
    // 重置资源限制配置
    containerLimitCpu: false,
    containerLimitMemory: false,
    containerLimitDisk: true,
    vmLimitCpu: true,
    vmLimitMemory: true,
    vmLimitDisk: true,
    // 重置等级限制配置
    levelLimits: {}
  })
  // 清空连接测试结果
  connectionTestResult.value = null
}

const submitAddServer = async () => {
  try {
    await addProviderFormRef.value.validate()
    
    // 验证虚拟化类型
    if (!validateVirtualizationType()) {
      return
    }
    
    // 验证SSH认证方式
    // 创建模式：必须提供认证信息
    // 编辑模式：留空表示不修改，只有在切换认证方式时才需要验证
    if (!isEditing.value) {
      // 创建模式：必须提供认证信息
      if (addProviderForm.authMethod === 'password' && !addProviderForm.password) {
        ElMessage.error(t('admin.providers.passwordRequired'))
        return
      }
      if (addProviderForm.authMethod === 'sshKey' && !addProviderForm.sshKey) {
        ElMessage.error(t('admin.providers.sshKeyRequired'))
        return
      }
    }
    
    addProviderLoading.value = true

    const serverData = {
      name: addProviderForm.name,
      type: addProviderForm.type,
      endpoint: addProviderForm.host, // 只存储主机地址
      portIP: addProviderForm.portIP, // 端口映射使用的公网IP
      sshPort: addProviderForm.port, // 单独存储SSH端口
      username: addProviderForm.username,
      // 注意：密码和SSH密钥根据认证方式在后面单独处理，这里不设置
      config: '',
      region: addProviderForm.region,
      country: addProviderForm.country,
      countryCode: addProviderForm.countryCode,
      city: addProviderForm.city,
      container_enabled: addProviderForm.containerEnabled,
      vm_enabled: addProviderForm.vmEnabled,
      architecture: addProviderForm.architecture, // 架构字段
      totalQuota: 0,
      allowClaim: true,
      status: addProviderForm.status, // 节点状态（active/offline/frozen）
      expiresAt: addProviderForm.expiresAt || '', // 过期时间字段
      maxContainerInstances: addProviderForm.maxContainerInstances || 0, // 最大容器数
      maxVMInstances: addProviderForm.maxVMInstances || 0, // 最大虚拟机数
      allowConcurrentTasks: addProviderForm.allowConcurrentTasks, // 是否允许并发任务
      maxConcurrentTasks: addProviderForm.maxConcurrentTasks || 1, // 最大并发任务数
      taskPollInterval: addProviderForm.taskPollInterval || 60, // 任务轮询间隔
      enableTaskPolling: addProviderForm.enableTaskPolling !== undefined ? addProviderForm.enableTaskPolling : true, // 是否启用任务轮询
      // 存储配置（ProxmoxVE专用）
      storagePool: addProviderForm.storagePool || 'local', // 存储池名称
      // 端口映射配置
      defaultPortCount: addProviderForm.defaultPortCount || 10,
      portRangeStart: addProviderForm.portRangeStart || 10000,
      portRangeEnd: addProviderForm.portRangeEnd || 65535,
      networkType: addProviderForm.networkType || 'nat_ipv4', // 网络配置类型
      // 带宽配置
      defaultInboundBandwidth: addProviderForm.defaultInboundBandwidth || 300,
      defaultOutboundBandwidth: addProviderForm.defaultOutboundBandwidth || 300,
      maxInboundBandwidth: addProviderForm.maxInboundBandwidth || 1000,
      maxOutboundBandwidth: addProviderForm.maxOutboundBandwidth || 1000,
      // 流量配置
      maxTraffic: addProviderForm.maxTraffic || 1048576,
      trafficCountMode: addProviderForm.trafficCountMode || 'both', // 流量统计模式
      trafficMultiplier: addProviderForm.trafficMultiplier !== undefined && addProviderForm.trafficMultiplier !== null ? addProviderForm.trafficMultiplier : 1.0, // 流量计费倍率
      // 操作执行规则
      executionRule: addProviderForm.executionRule || 'auto', // 操作轮转规则
      // SSH超时配置
      sshConnectTimeout: addProviderForm.sshConnectTimeout || 30,
      sshExecuteTimeout: addProviderForm.sshExecuteTimeout || 300,
      // 容器资源限制配置
      containerLimitCpu: addProviderForm.containerLimitCpu !== undefined ? addProviderForm.containerLimitCpu : false,
      containerLimitMemory: addProviderForm.containerLimitMemory !== undefined ? addProviderForm.containerLimitMemory : false,
      containerLimitDisk: addProviderForm.containerLimitDisk !== undefined ? addProviderForm.containerLimitDisk : true,
      // 虚拟机资源限制配置
      vmLimitCpu: addProviderForm.vmLimitCpu !== undefined ? addProviderForm.vmLimitCpu : true,
      vmLimitMemory: addProviderForm.vmLimitMemory !== undefined ? addProviderForm.vmLimitMemory : true,
      vmLimitDisk: addProviderForm.vmLimitDisk !== undefined ? addProviderForm.vmLimitDisk : true,
      // 节点等级限制配置
      levelLimits: addProviderForm.levelLimits || {}
    }

    // 根据Provider类型设置端口映射方式
    // Docker 类型固定使用 native，不可选择
    if (addProviderForm.type === 'docker') {
      serverData.ipv4PortMappingMethod = 'native'
      serverData.ipv6PortMappingMethod = 'native'
    } else if (addProviderForm.type === 'proxmox') {
      // Proxmox IPv4: NAT情况下默认iptables，独立IP情况下可选
      if (addProviderForm.networkType === 'nat_ipv4' || addProviderForm.networkType === 'nat_ipv4_ipv6') {
        serverData.ipv4PortMappingMethod = addProviderForm.ipv4PortMappingMethod || 'iptables'
      } else {
        serverData.ipv4PortMappingMethod = addProviderForm.ipv4PortMappingMethod || 'native'
      }
      // Proxmox IPv6: 默认native，可选iptables
      serverData.ipv6PortMappingMethod = addProviderForm.ipv6PortMappingMethod || 'native'
    } else if (['lxd', 'incus'].includes(addProviderForm.type)) {
      // LXD/Incus IPv4默认device_proxy
      serverData.ipv4PortMappingMethod = addProviderForm.ipv4PortMappingMethod || 'device_proxy'
      // LXD/Incus IPv6默认device_proxy，可选iptables
      serverData.ipv6PortMappingMethod = addProviderForm.ipv6PortMappingMethod || 'device_proxy'
    }

    // 密码和SSH密钥的处理逻辑
    if (isEditing.value) {
      // 编辑模式：根据当前选择的认证方式和是否填写了新凭证来决定
      const originalAuthMethod = addProviderForm.authMethod // 当前选择的认证方式
      
      if (originalAuthMethod === 'password') {
        // 当前选择密码认证
        if (addProviderForm.password) {
          // 填写了新密码，更新密码
          serverData.password = addProviderForm.password
        }
        // 如果原来是SSH密钥认证，但现在切换到密码，需要确保填写了密码
        // 不主动清空SSH密钥，让后端根据优先级处理
      } else if (originalAuthMethod === 'sshKey') {
        // 当前选择SSH密钥认证
        if (addProviderForm.sshKey) {
          // 填写了新SSH密钥，更新SSH密钥
          serverData.sshKey = addProviderForm.sshKey
        }
        // 如果原来是密码认证，但现在切换到SSH密钥，需要确保填写了SSH密钥
        // 不主动清空密码，让后端根据优先级处理（SSH密钥优先于密码）
      }
    } else {
      // 创建模式：根据认证方式发送对应字段，确保只发送一种
      if (addProviderForm.authMethod === 'password') {
        serverData.password = addProviderForm.password
        // 创建时不发送sshKey，避免发送空字符串
      } else if (addProviderForm.authMethod === 'sshKey') {
        serverData.sshKey = addProviderForm.sshKey
        // 创建时不发送password，避免发送空字符串
      }
    }

    if (isEditing.value) {
      await updateProvider(addProviderForm.id, serverData)
      ElMessage.success(t('admin.providers.serverUpdateSuccess'))
    } else {
      // 服务器
      await createProvider(serverData)
      ElMessage.success(t('admin.providers.serverAddSuccess'))
    }
    
    cancelAddServer()
    await loadProviders()
  } catch (error) {
    console.error('Provider操作失败:', error)
    const errorMsg = error.response?.data?.msg || error.message || (isEditing.value ? t('admin.providers.serverUpdateFailed') : t('admin.providers.serverAddFailed'))
    ElMessage.error(errorMsg)
  } finally {
    addProviderLoading.value = false
  }
}

const editProvider = (provider) => {
  // 获取主机地址，如果endpoint包含端口则分离，否则使用完整地址
  let host = provider.endpoint
  if (provider.endpoint && provider.endpoint.includes(':')) {
    host = provider.endpoint.split(':')[0]
  }
  
  // 使用数据库中的sshPort字段，如果没有则默认为22
  const port = provider.sshPort || 22
  
  // 填充表单数据
  Object.assign(addProviderForm, {
    id: provider.id,
    name: provider.name,
    type: provider.type,
    host: host,
    portIP: provider.portIP || '', // 端口IP
    port: parseInt(port) || 22,
    username: provider.username || '',
    password: '', // 编辑时不显示密码，留空表示不修改
    sshKey: '', // 编辑时不显示SSH密钥，留空表示不修改
    authMethod: provider.authMethod || 'password', // 使用后端返回的认证方式
    description: provider.description || '',
    region: provider.region || '',
    country: provider.country || '',
    countryCode: provider.countryCode || '',
    city: provider.city || '',
    containerEnabled: provider.container_enabled === true,
    vmEnabled: provider.vm_enabled === true,
    architecture: provider.architecture || 'amd64', // 架构字段
    status: provider.status || 'active',
    expiresAt: provider.expiresAt || '', // 过期时间字段
    maxContainerInstances: provider.maxContainerInstances || 0, // 最大容器数
    maxVMInstances: provider.maxVMInstances || 0, // 最大虚拟机数
    allowConcurrentTasks: provider.allowConcurrentTasks || false, // 是否允许并发任务
    maxConcurrentTasks: provider.maxConcurrentTasks || 1, // 最大并发任务数
    taskPollInterval: provider.taskPollInterval || 60, // 任务轮询间隔
    enableTaskPolling: provider.enableTaskPolling !== undefined ? provider.enableTaskPolling : true, // 是否启用任务轮询
    // 存储配置（ProxmoxVE专用）
    storagePool: provider.storagePool || 'local', // 存储池名称
    // 端口映射配置
    defaultPortCount: provider.defaultPortCount || 10,
    enableIPv6: provider.enableIPv6 || false, // 兼容字段
    portRangeStart: provider.portRangeStart || 10000,
    portRangeEnd: provider.portRangeEnd || 65535,
    networkType: provider.networkType || 'nat_ipv4', // 网络配置类型
    // 带宽配置
    defaultInboundBandwidth: provider.defaultInboundBandwidth || 300,
    defaultOutboundBandwidth: provider.defaultOutboundBandwidth || 300,
    maxInboundBandwidth: provider.maxInboundBandwidth || 1000,
    maxOutboundBandwidth: provider.maxOutboundBandwidth || 1000,
    // 流量配置
    maxTraffic: provider.maxTraffic || 1048576,
    trafficCountMode: provider.trafficCountMode || 'both', // 流量统计模式
    trafficMultiplier: provider.trafficMultiplier || 1.0, // 流量倍率
    // 操作执行规则
    executionRule: provider.executionRule || 'auto', // 默认自动切换
    // SSH超时配置
    sshConnectTimeout: provider.sshConnectTimeout || 30,
    sshExecuteTimeout: provider.sshExecuteTimeout || 300,
    // 容器资源限制配置
    containerLimitCpu: provider.containerLimitCpu !== undefined ? provider.containerLimitCpu : false,
    containerLimitMemory: provider.containerLimitMemory !== undefined ? provider.containerLimitMemory : false,
    containerLimitDisk: provider.containerLimitDisk !== undefined ? provider.containerLimitDisk : true,
    // 虚拟机资源限制配置
    vmLimitCpu: provider.vmLimitCpu !== undefined ? provider.vmLimitCpu : true,
    vmLimitMemory: provider.vmLimitMemory !== undefined ? provider.vmLimitMemory : true,
    vmLimitDisk: provider.vmLimitDisk !== undefined ? provider.vmLimitDisk : true,
    // 节点等级限制配置 - 从后端解析JSON或使用默认值
    levelLimits: parseLevelLimits(provider.levelLimits)
  })

  // 根据Provider类型设置端口映射方式，优先使用数据库中保存的值，没有时使用类型默认值
  // Docker 类型固定为 native
  if (provider.type === 'docker') {
    addProviderForm.ipv4PortMappingMethod = 'native'
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (provider.type === 'proxmox') {
    addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'iptables'
    addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'native'
  } else if (['lxd', 'incus'].includes(provider.type)) {
    addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'device_proxy'
  } else {
    addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'device_proxy'
  }
  
  isEditing.value = true
  showAddDialog.value = true
  
  // 使用 nextTick 确保弹窗打开后，表单数据正确绑定到组件
  nextTick(() => {
    // 强制更新复选框状态
    addProviderForm.containerEnabled = provider.container_enabled === true
    addProviderForm.vmEnabled = provider.vm_enabled === true
  })
}

const handleDeleteProvider = async (id) => {
  try {
    await ElMessageBox.confirm(
      '此操作将永久删除该服务器，是否继续？',
      '警告',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    await deleteProvider(id)
    ElMessage.success(t('admin.providers.serverDeleteSuccess'))
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      // 显示后端返回的具体错误信息
      const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.serverDeleteFailed')
      ElMessage.error(errorMsg)
    }
  }
}

const freezeServer = async (id) => {
  try {
    await ElMessageBox.confirm(
      '此操作将冻结该服务器，冻结后普通用户无法使用该服务器创建实例，是否继续？',
      '确认冻结',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    await freezeProvider(id)
    ElMessage.success(t('admin.providers.serverFrozen'))
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.providers.serverFreezeFailed'))
    }
  }
}

const unfreezeServer = async (server) => {
  try {
    const { value: expiresAt } = await ElMessageBox.prompt(
      '请输入新的过期时间（格式：YYYY-MM-DD HH:MM:SS 或 YYYY-MM-DD），留空则默认设置为31天后过期',
      '解冻服务器',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        inputPattern: /^(\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?)?$/,
        inputErrorMessage: t('admin.providers.validation.dateFormatError'),
        inputPlaceholder: '如：2024-12-31 23:59:59 或留空'
      }
    )

    await unfreezeProvider(server.id, expiresAt || '')
    ElMessage.success(t('admin.providers.serverUnfrozen'))
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.providers.serverUnfreezeFailed'))
    }
  }
}

const handleSizeChange = (newSize) => {
  pageSize.value = newSize
  currentPage.value = 1
  loadProviders()
}

const handleCurrentChange = (newPage) => {
  currentPage.value = newPage
  loadProviders()
}

// 监听provider类型变化，自动设置虚拟化类型支持和端口映射方式
watch(() => addProviderForm.type, (newType) => {
  if (newType === 'docker') {
    // Docker只支持容器，使用原生端口映射
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = false
    addProviderForm.ipv4PortMappingMethod = 'native' // Docker使用原生实现
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (newType === 'proxmox') {
    // Proxmox支持容器和虚拟机
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = true
    // IPv4: NAT模式下默认iptables，独立IP模式下默认native
    const isNATMode = addProviderForm.networkType === 'nat_ipv4' || addProviderForm.networkType === 'nat_ipv4_ipv6'
    addProviderForm.ipv4PortMappingMethod = isNATMode ? 'iptables' : 'native'
    // IPv6: 默认native
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (['lxd', 'incus'].includes(newType)) {
    // LXD/Incus支持容器和虚拟机，默认使用device_proxy
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = true
    addProviderForm.ipv4PortMappingMethod = 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = 'device_proxy'
  } else {
    // 其他类型保持默认设置
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = false
    addProviderForm.ipv4PortMappingMethod = 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = 'device_proxy'
  }
})

// 监听网络类型变化，当Proxmox从NAT改为独立IP时，自动调整端口映射方法
watch(() => [addProviderForm.type, addProviderForm.networkType], ([type, networkType]) => {
  if (type === 'proxmox') {
    const isNATMode = networkType === 'nat_ipv4' || networkType === 'nat_ipv4_ipv6'
    if (isNATMode) {
      // NAT模式只能使用iptables
      addProviderForm.ipv4PortMappingMethod = 'iptables'
    } else {
      // 独立IP模式默认使用native，但也可以选择iptables
      if (addProviderForm.ipv4PortMappingMethod === 'iptables') {
        // 如果当前是iptables，保持不变
      } else {
        addProviderForm.ipv4PortMappingMethod = 'native'
      }
    }
  }
})

// 格式化流量大小
const formatTraffic = (sizeInMB) => {
  if (!sizeInMB || sizeInMB === 0) return '0B'
  
  const units = ['MB', 'GB', 'TB', 'PB']
  let size = sizeInMB
  let unitIndex = 0
  
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex++
  }
  
  return `${size.toFixed(unitIndex === 0 ? 0 : 1)}${units[unitIndex]}`
}

// 计算流量使用百分比
const getTrafficPercentage = (used, max) => {
  if (!max || max === 0) return 0
  return Math.min(Math.round((used / max) * 100), 100)
}

// 获取流量进度条状态
const getTrafficProgressStatus = (used, max) => {
  const percentage = getTrafficPercentage(used, max)
  if (percentage >= 90) return 'exception'
  if (percentage >= 80) return 'warning'
  return 'success'
}

// 格式化日期时间
const formatDateTime = (dateTimeStr) => {
  if (!dateTimeStr) return '-'
  const date = new Date(dateTimeStr)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

// 检查是否过期
const isExpired = (dateTimeStr) => {
  if (!dateTimeStr) return false
  return new Date(dateTimeStr) < new Date()
}

// 检查是否即将过期（7天内）
const isNearExpiry = (dateTimeStr) => {
  if (!dateTimeStr) return false
  const expiryDate = new Date(dateTimeStr)
  const now = new Date()
  const diffDays = (expiryDate - now) / (1000 * 60 * 60 * 24)
  return diffDays <= 7 && diffDays > 0
}

// 获取状态类型（用于el-tag的type属性）
const getStatusType = (status) => {
  switch (status) {
    case 'online':
      return 'success'
    case 'offline':
      return 'danger'
    case 'unknown':
    default:
      return 'info'
  }
}

// 获取状态文本
const getStatusText = (status) => {
  switch (status) {
    case 'online':
      return '在线'
    case 'offline':
      return '离线'
    case 'unknown':
    default:
      return '未知'
  }
}

// 自动配置API相关状态
const configDialog = reactive({
  visible: false,
  provider: null,
  showHistory: false,
  runningTask: null,
  historyTasks: []
})

// 任务日志查看对话框状态
const taskLogDialog = reactive({
  visible: false,
  loading: false,
  error: null,
  task: null
})

// 自动配置API - 新版本
const autoConfigureAPI = async (provider, force = false) => {
  try {
    // 先调用检查API
    const checkResponse = await autoConfigureProvider({
      providerId: provider.id,
      showHistory: true
    })

    const result = checkResponse.data

    // 如果有正在运行的任务或历史记录
    if (result.runningTask || (result.historyTasks && result.historyTasks.length > 0)) {
      // 显示历史记录对话框
      configDialog.provider = provider
      configDialog.runningTask = result.runningTask
      configDialog.historyTasks = result.historyTasks || []
      configDialog.showHistory = true
      configDialog.visible = true
      
      // 如果有正在运行的任务，直接查看任务日志
      if (result.runningTask) {
        ElMessage.info(t('admin.providers.showTaskLog'))
        await viewTaskLog(result.runningTask.id)
        return
      }
      
      return
    }

    // 如果没有历史记录，直接开始配置
    await startNewConfiguration(provider, force)

  } catch (error) {
    console.error('检查配置状态失败:', error)
    ElMessage.error(t('admin.providers.checkConfigFailed') + ': ' + (error.message || t('common.unknownError')))
  }
}

// 开始新的配置
const startNewConfiguration = async (provider, force = false) => {
  try {
    const confirmMessage = force ? 
      '确定要强制重新配置吗？这将取消当前正在运行的任务。' :
      `确定要自动配置 ${provider.name} (${provider.type.toUpperCase()}) 的API访问吗？<br>
<strong>此操作将：</strong><br>
• 通过SSH连接到服务器<br>
• 自动安装和配置必要的证书/Token<br>
• 清理其他控制端的配置<br>
• 确保只有当前控制端能管理该服务器<br><br>
<span style="color: #E6A23C;">请确保SSH连接信息正确且用户有足够权限。</span>`

    await ElMessageBox.confirm(
      confirmMessage,
      force ? '确认强制配置' : '确认自动配置',
      {
        confirmButtonText: force ? '强制配置' : '确定配置',
        cancelButtonText: '取消',
        type: 'warning',
        dangerouslyUseHTMLString: true
      }
    )

    // 显示加载提示
    const loadingMessage = ElMessage({
      message: t('admin.providers.validation.autoConfiguring'),
      type: 'info',
      duration: 0,
      showClose: false
    })

    try {
      // 开始配置
      const response = await autoConfigureProvider({
        providerId: provider.id,
        force
      })

      const result = response.data

      // 关闭加载提示
      loadingMessage.close()

      // 配置完成后直接显示结果
      if (result.taskId) {
        // 直接查看任务日志
        await viewTaskLog(result.taskId)
        // 重新加载服务器列表
        await loadProviders()
      } else {
        ElMessage.success('API 自动配置成功')
        await loadProviders()
      }

    } catch (configError) {
      loadingMessage.close()
      throw configError
    }

  } catch (error) {
    if (error !== 'cancel') {
      console.error('启动配置失败:', error)
      ElMessage.error(t('admin.providers.startConfigFailed') + ': ' + (error.message || t('common.unknownError')))
    }
  }
}

// 重新执行配置
const rerunConfiguration = () => {
  configDialog.visible = false
  startNewConfiguration(configDialog.provider, true)
}

// 查看运行中的任务
const viewRunningTask = () => {
  if (configDialog.runningTask) {
    // 直接查看任务日志（只支持最终日志）
    viewTaskLog(configDialog.runningTask.id)
  }
}

// 获取任务状态类型
const getTaskStatusType = (status) => {
  switch (status) {
    case 'completed':
      return 'success'
    case 'failed':
      return 'danger'
    case 'running':
      return 'warning'
    case 'cancelled':
      return 'info'
    default:
      return 'info'
  }
}

// 获取任务状态文本
const getTaskStatusText = (status) => {
  switch (status) {
    case 'completed':
      return '已完成'
    case 'failed':
      return '失败'
    case 'running':
      return '运行中'
    case 'cancelled':
      return '已取消'
    case 'pending':
      return '等待中'
    default:
      return '未知'
  }
}

// 调试函数：检查认证状态
const debugAuthStatus = () => {
  const userStore = useUserStore()
  console.log('Debug Auth Status:')
  console.log('- UserStore token:', userStore.token ? 'exists' : 'not found')
  console.log('- SessionStorage token:', sessionStorage.getItem('token') ? 'exists' : 'not found')
  console.log('- User type:', userStore.userType)
  console.log('- Is logged in:', userStore.isLoggedIn)
  console.log('- Is admin:', userStore.isAdmin)
}

// 健康检查
const checkHealth = async (providerId) => {
  const loadingMessage = ElMessage({
    message: t('admin.providers.validation.healthChecking'),
    type: 'info',
    duration: 0, // 不自动关闭
    showClose: false
  })
  
  try {
    console.log('开始健康检查，Provider ID:', providerId)
    const result = await checkProviderHealth(providerId)
    console.log('健康检查API返回结果:', result)
    
    loadingMessage.close() // 关闭加载消息
    
    if (result.code === 200) {
      console.log('健康检查成功，显示成功消息')
      ElMessage.success(t('admin.providers.healthCheckComplete'))
      await loadProviders() // 重新加载提供商列表以更新状态
    } else {
      console.log('健康检查失败，code:', result.code, 'msg:', result.msg)
      ElMessage.error(result.msg || t('admin.providers.healthCheckFailed'))
    }
  } catch (error) {
    loadingMessage.close() // 确保关闭加载消息
    console.error('健康检查异常:', error)
    console.log('异常详情:', {
      message: error.message,
      response: error.response,
      stack: error.stack
    })
    
    // 优化错误消息显示
    let errorMsg = '健康检查失败'
    if (error.message && error.message.includes('timeout')) {
      errorMsg = '健康检查超时，请检查网络连接或服务器状态'
    } else if (error.message) {
      errorMsg = '健康检查失败: ' + error.message
    }
    
    ElMessage.error(errorMsg)
  }
}

onMounted(() => {
  // 在开发环境下输出调试信息
  if (import.meta.env.DEV) {
    debugAuthStatus()
  }
  loadProviders()
})

// 查看任务日志
const viewTaskLog = async (taskId) => {
  taskLogDialog.visible = true
  taskLogDialog.loading = true
  taskLogDialog.error = null
  taskLogDialog.task = null

  try {
    const response = await getConfigurationTaskDetail(taskId)
    console.log('任务详情API响应:', response) // 调试日志
    if (response.code === 0 || response.code === 200) {
      taskLogDialog.task = response.data
    } else {
      taskLogDialog.error = response.msg || '获取任务详情失败'
    }
  } catch (error) {
    console.error('获取任务日志失败:', error)
    taskLogDialog.error = '获取任务日志失败: ' + (error.message || '未知错误')
  } finally {
    taskLogDialog.loading = false
  }
}

// 复制任务日志
const copyTaskLog = async () => {
  const logOutput = taskLogDialog.task?.logOutput
  if (!logOutput) {
    ElMessage.warning(t('admin.providers.noLogToCopy'))
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(logOutput)
      ElMessage.success(t('admin.providers.logCopied'))
      return
    }
    
    // 降级方案：使用传统的 document.execCommand
    const textArea = document.createElement('textarea')
    textArea.value = logOutput
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    
    try {
      // @ts-ignore - execCommand 已废弃但作为降级方案仍需使用
      const successful = document.execCommand('copy')
      if (successful) {
        ElMessage.success(t('admin.providers.logCopied'))
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error(t('admin.providers.copyFailed'))
  }
}

// 格式化相对时间
const formatRelativeTime = (dateTime) => {
  if (!dateTime) return ''
  
  const now = new Date()
  const date = new Date(dateTime)
  const diffInMinutes = Math.floor((now - date) / (1000 * 60))
  
  if (diffInMinutes < 1) return '刚刚'
  if (diffInMinutes < 60) return `${diffInMinutes}分钟前`
  
  const diffInHours = Math.floor(diffInMinutes / 60)
  if (diffInHours < 24) return `${diffInHours}小时前`
  
  const diffInDays = Math.floor(diffInHours / 24)
  if (diffInDays < 7) return `${diffInDays}天前`
  
  return date.toLocaleDateString()
}
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: #303133;
  }
}

.filter-container {
  margin-bottom: 20px;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.support-type-group {
  display: flex;
  gap: 15px;
}

.form-tip {
  margin-top: 5px;
}

/* 服务器配置标签页样式 */
.server-config-tabs {
  margin-bottom: 20px;
}

.server-config-tabs .el-tab-pane {
  padding: 20px 0;
}

.server-form {
  max-height: 400px;
  overflow-y: auto;
  padding-right: 10px;
}

.location-cell {
  display: flex;
  align-items: center;
  gap: 5px;
}

.flag-icon {
  font-size: 16px;
}

.support-types {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.el-select .el-input {
  width: 100%;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.connection-status {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.resource-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
}

.resource-item {
  display: flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

.resource-item .el-icon {
  font-size: 14px;
  color: #909399;
}

.resource-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 60px;
  color: #c0c4cc;
}

.sync-time {
  margin-top: 2px;
  text-align: center;
}

.traffic-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}

.traffic-usage {
  display: flex;
  align-items: center;
  gap: 2px;
  font-weight: 500;
}

.traffic-usage .separator {
  color: #c0c4cc;
  margin: 0 2px;
}

.traffic-progress {
  width: 100%;
}

.traffic-status {
  text-align: center;
}

/* 资源限制配置样式 */
.resource-limit-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 20px;
  background: #f5f7fa;
  border-radius: 8px;
  transition: all 0.3s;
}

.resource-limit-item:hover {
  background: #ecf0f3;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}

.resource-limit-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}

.resource-limit-label .el-icon {
  font-size: 18px;
  color: #409eff;
}

.resource-limit-tip {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: #909399;
  text-align: center;
}

.resource-limit-tip .el-icon {
  color: #409eff;
}

/* 等级限制配置样式 */
.level-limits-container {
  padding: 10px;
  max-height: 450px;
  overflow-y: auto;
}

/* 自定义滚动条样式 */
.level-limits-container::-webkit-scrollbar {
  width: 8px;
}

.level-limits-container::-webkit-scrollbar-track {
  background: #f1f1f1;
  border-radius: 4px;
}

.level-limits-container::-webkit-scrollbar-thumb {
  background: #c0c4cc;
  border-radius: 4px;
}

.level-limits-container::-webkit-scrollbar-thumb:hover {
  background: #909399;
}

.level-config-card {
  margin-bottom: 16px;
  padding: 16px;
  background: #f8f9fa;
  border-radius: 6px;
  border: 1px solid #e4e7ed;
  transition: all 0.3s;
}

.level-config-card:hover {
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
  border-color: #c0c4cc;
}

.level-header {
  margin-bottom: 12px;
  padding-bottom: 8px;
  border-bottom: 2px solid #e4e7ed;
}

.level-form {
  margin-top: 8px;
}

.level-form .el-form-item {
  margin-bottom: 12px;
}

.level-form .el-divider {
  margin: 12px 0;
}

.level-form .form-tip {
  margin-top: 2px;
}
</style>
<template>
  <div class="instance-detail">
    <!-- 页面头部 -->
    <div class="page-header">
      <el-button 
        type="text" 
        class="back-btn"
        @click="$router.back()"
      >
        <el-icon><ArrowLeft /></el-icon>
        {{ t('user.instanceDetail.backToList') }}
      </el-button>
    </div>

    <!-- 实例概览卡片 -->
    <el-card class="overview-card">
      <!-- 关联任务提示 -->
      <el-alert
        v-if="instance.relatedTask"
        :title="getTaskTitle(instance.relatedTask)"
        :type="getTaskAlertType(instance.relatedTask.status)"
        :description="instance.relatedTask.statusMessage || $t('user.instanceDetail.taskProgress', { progress: instance.relatedTask.progress })"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      >
        <template #default>
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <div>
              <p>{{ instance.relatedTask.statusMessage || $t('user.instanceDetail.taskInProgress', { action: getTaskTypeText(instance.relatedTask.taskType) }) }}</p>
              <el-progress 
                :percentage="instance.relatedTask.progress" 
                :status="instance.relatedTask.progress === 100 ? 'success' : undefined"
                style="margin-top: 10px;"
              />
            </div>
            <el-button 
              type="primary" 
              size="small"
              @click="viewTaskDetail(instance.relatedTask.id)"
            >
              {{ t('user.instanceDetail.viewTaskDetail') }}
            </el-button>
          </div>
        </template>
      </el-alert>
      
      <!-- Provider离线警告 -->
      <el-alert
        v-if="instance.providerStatus && (instance.providerStatus === 'inactive' || instance.providerStatus === 'partial')"
        :title="t('user.instanceDetail.providerOfflineWarning')"
        type="error"
        :description="t('user.instanceDetail.providerOfflineDesc')"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      />
      
      <!-- 实例不可用警告 -->
      <el-alert
        v-if="instance.status === 'unavailable'"
        :title="t('user.instanceDetail.instanceUnavailableWarning')"
        type="warning"
        :description="t('user.instanceDetail.instanceUnavailableDesc')"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      />
      
      <div class="server-overview">
        <!-- 左侧：实例基本信息 -->
        <div class="server-basic-info">
          <div class="server-header">
            <div class="server-name-section">
              <h1 class="server-name">
                {{ instance.name }}
              </h1>
              <div class="server-meta">
                <el-tag
                  :type="instance.instance_type === 'vm' ? 'primary' : 'success'"
                  size="small"
                >
                  {{ instance.instance_type === 'vm' ? t('user.instanceDetail.vm') : t('user.instanceDetail.container') }}
                </el-tag>
                <el-tag 
                  v-if="instance.providerType"
                  :type="getProviderTypeColor(instance.providerType)"
                  size="small"
                  style="margin-left: 8px;"
                >
                  {{ getProviderTypeName(instance.providerType) }}
                </el-tag>
                <span class="server-provider">{{ instance.providerName }}</span>
              </div>
            </div>
            <div class="server-status">
              <el-tag 
                :type="getStatusType(instance.status)"
                effect="dark"
                size="large"
              >
                {{ getStatusText(instance.status) }}
              </el-tag>
            </div>
          </div>
          
          <!-- 实例控制按钮 - 移到名称下方 -->
          <div class="control-actions">
            <el-tooltip
              v-if="instance.status === 'stopped'"
              :content="monitoring.trafficData?.isLimited ? t('user.instanceDetail.trafficLimitStartBlocked') : ''"
              :disabled="!monitoring.trafficData?.isLimited"
              placement="top"
            >
              <span>
                <el-button 
                  type="success" 
                  size="small"
                  :loading="actionLoading"
                  :disabled="monitoring.trafficData?.isLimited"
                  @click="performAction('start')"
                >
                  <el-icon><VideoPlay /></el-icon>
                  {{ t('user.instanceDetail.start') }}
                </el-button>
              </span>
            </el-tooltip>
            <el-button 
              v-if="instance.status === 'running'"
              type="warning" 
              size="small"
              :loading="actionLoading"
              @click="performAction('stop')"
            >
              <el-icon><VideoPause /></el-icon>
              {{ t('user.instanceDetail.stop') }}
            </el-button>
            <el-button 
              v-if="instance.status === 'running' && instance.canRestart !== false"
              size="small"
              :loading="actionLoading"
              @click="performAction('restart')"
            >
              <el-icon><Refresh /></el-icon>
              {{ t('user.instanceDetail.restart') }}
            </el-button>
            <el-button 
              v-if="instanceTypePermissions.canResetInstance"
              type="info"
              size="small"
              :loading="actionLoading"
              @click="performAction('reset')"
            >
              <el-icon><Refresh /></el-icon>
              {{ t('user.instanceDetail.resetSystem') }}
            </el-button>
            <el-button 
              v-if="instance.status === 'running'"
              type="primary"
              size="small"
              :loading="actionLoading"
              @click="showResetPasswordDialog"
            >
              {{ t('user.instanceDetail.resetPassword') }}
            </el-button>
            <!-- Web SSH按钮 -->
            <el-button 
              v-if="instance.status === 'running' && instance.password"
              type="primary"
              size="small"
              :disabled="!instance.hasSshMapping && instance.networkType === 'no_port_mapping'"
              :title="(!instance.hasSshMapping && instance.networkType === 'no_port_mapping') ? t('user.instanceDetail.sshNoPortMapping') : ''"
              @click="openSSHTerminal"
            >
              <el-icon><Monitor /></el-icon>
              {{ t('user.instanceDetail.webSSH') }}
            </el-button>
            <!-- Container Exec按钮 -->
            <el-button 
              v-if="instance.status === 'running' && instance.instance_type === 'container'"
              type="success"
              size="small"
              @click="openExecTerminal"
            >
              <el-icon><Monitor /></el-icon>
              {{ t('user.instanceDetail.webExec') }}
            </el-button>
            <!-- 删除按钮 - 根据权限显示 -->
            <el-button 
              v-if="instanceTypePermissions.canDeleteInstance"
              type="danger"
              size="small"
              :loading="actionLoading"
              @click="performAction('delete')"
            >
              <el-icon><Delete /></el-icon>
              {{ t('user.instanceDetail.delete') }}
            </el-button>
          </div>
        </div>

        <!-- 右侧：硬件信息 -->
        <div class="server-hardware">
          <h3>{{ t('user.instanceDetail.hardware') }}</h3>
          <div class="hardware-grid">
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.cpu') }}</span>
              <span class="value">{{ instance.cpu }}{{ t('user.instanceDetail.core') }}</span>
            </div>
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.memory') }}</span>
              <span class="value">{{ formatMemorySize(instance.memory) }}</span>
            </div>
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.storage') }}</span>
              <span class="value">{{ formatDiskSize(instance.disk) }}</span>
            </div>
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.bandwidth') }}</span>
              <span class="value">{{ instance.bandwidth }}Mbps</span>
            </div>
          </div>
        </div>
      </div>
    </el-card>

    <!-- 标签页内容 -->
    <el-card class="tabs-card">
      <el-tabs
        v-model="activeTab"
        type="border-card"
      >
        <!-- 概览标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.overview')"
          name="overview"
        >
          <div class="overview-content">
            <!-- SSH连接信息 -->
            <div class="connection-section">
              <h3>{{ t('user.instanceDetail.sshConnection') }}</h3>
              <div class="connection-grid">
                <div class="connection-item">
                  <span class="label">{{ t('user.instanceDetail.publicIPv4') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.publicIP || t('user.instanceDetail.none')"
                    >
                      {{ truncateIP(instance.publicIP) || t('user.instanceDetail.none') }}
                    </span>
                    <el-button 
                      v-if="instance.publicIP"
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.publicIP)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div 
                  v-if="instance.privateIP"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.privateIPv4') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.privateIP"
                    >
                      {{ truncateIP(instance.privateIP) }}
                    </span>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.privateIP)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div 
                  v-if="instance.ipv6Address"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.ipv6') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.ipv6Address"
                    >
                      {{ truncateIP(instance.ipv6Address) }}
                    </span>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.ipv6Address)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div 
                  v-if="instance.publicIPv6"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.ipv6') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.publicIPv6"
                    >
                      {{ truncateIP(instance.publicIPv6) }}
                    </span>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.publicIPv6)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div class="connection-item">
                  <span class="label">{{ t('user.instanceDetail.sshPort') }}</span>
                  <div class="value-with-action">
                    <span class="value">{{ instance.sshPort || 22 }}</span>
                    <el-button 
                      v-if="instance.sshPort"
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.sshPort.toString())"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div class="connection-item">
                  <span class="label">{{ t('user.instanceDetail.username') }}</span>
                  <div class="value-with-action">
                    <span class="value">{{ instance.username || 'root' }}</span>
                    <el-button 
                      v-if="instance.username"
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.username)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div
                  v-if="instance.password"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.password') }}</span>
                  <div class="value-with-action">
                    <span class="value">{{ showPassword ? instance.password : '••••••••' }}</span>
                    <el-button 
                      size="small" 
                      text 
                      @click="togglePassword"
                    >
                      {{ showPassword ? t('user.instanceDetail.hide') : t('user.instanceDetail.show') }}
                    </el-button>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.password)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
              </div>
            </div>

            <!-- 基本信息 -->
            <div class="basic-info-section">
              <h3>{{ t('user.instanceDetail.basicInfo') }}</h3>
              <div class="info-grid">
                <div class="info-item">
                  <span class="label">{{ t('user.instanceDetail.os') }}</span>
                  <span class="value"><OsIcon
                    :name="instance.osType || instance.image"
                    :size="20"
                    style="margin-right: 6px;"
                  />{{ instance.osType }}</span>
                </div>
                <div class="info-item">
                  <span class="label">{{ t('user.instanceDetail.createdAt') }}</span>
                  <span class="value">{{ formatDate(instance.createdAt) }}</span>
                </div>
                <div class="info-item">
                  <span class="label">{{ t('user.instanceDetail.expiredAt') }}</span>
                  <span class="value">{{ formatDate(instance.expiresAt) }}</span>
                </div>
                <div
                  v-if="instance.networkType || instance.ipv4MappingType"
                  class="info-item"
                >
                  <span class="label">{{ t('user.instanceDetail.networkType') }}</span>
                  <el-tag
                    size="small"
                    :type="getNetworkTypeTagType(instance.networkType || getNetworkTypeFromLegacy(instance.ipv4MappingType, instance.ipv6Address))"
                  >
                    {{ getNetworkTypeDisplayName(instance.networkType || getNetworkTypeFromLegacy(instance.ipv4MappingType, instance.ipv6Address)) }}
                  </el-tag>
                </div>
                <!-- 保留旧字段显示以兼容性 -->
                <div
                  v-if="instance.ipv4MappingType && !instance.networkType"
                  class="info-item"
                  style="display: none"
                >
                  <span class="label">{{ t('user.instanceDetail.ipv4MappingTypeCompat') }}</span>
                  <el-tag
                    size="small"
                    :type="instance.ipv4MappingType === 'dedicated' ? 'success' : 'primary'"
                  >
                    {{ instance.ipv4MappingType === 'dedicated' ? t('user.instanceDetail.dedicatedIPv4') : t('user.instanceDetail.natSharedIP') }}
                  </el-tag>
                </div>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- 端口映射标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.portMapping')"
          name="ports"
        >
          <div class="ports-content">
            <div class="ports-header">
              <div class="ports-summary">
                <div class="summary-item">
                  <span class="label">{{ t('user.instanceDetail.publicIP') }}:</span>
                  <span class="value">{{ instance.publicIP || t('user.instanceDetail.none') }}</span>
                </div>
                <div class="summary-item">
                  <span class="label">{{ t('user.instances.portMapping') }}:</span>
                  <span class="value">{{ portMappings.length }}{{ t('common.items') }}</span>
                </div>
              </div>
              <el-button
                type="primary"
                size="small"
                @click="refreshPortMappings"
              >
                <el-icon><Refresh /></el-icon>
                {{ t('user.instances.search') }}
              </el-button>
            </div>
            
            <el-table
              v-if="portMappings && portMappings.length > 0"
              :data="portMappings"
              stripe
              class="ports-table"
            >
              <el-table-column
                prop="portType"
                :label="t('user.instanceDetail.portType')"
                width="110"
              >
                <template #default="{ row }">
                  <el-tag
                    size="small"
                    :type="row.portType === 'manual' ? 'warning' : 'success'"
                  >
                    {{ row.portType === 'manual' ? t('user.instanceDetail.manualAdd') : t('user.instanceDetail.rangeMapping') }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                prop="mappingType"
                :label="t('user.instanceDetail.mappingSource')"
                width="110"
              >
                <template #default="{ row }">
                  <el-tag
                    size="small"
                    :type="row.mappingType === 'controller' ? 'warning' : 'primary'"
                  >
                    {{ row.mappingType === 'controller' ? t('user.instanceDetail.controllerForwarding') : t('user.instanceDetail.nodeForwarding') }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                prop="hostPort"
                :label="t('user.instanceDetail.publicPort')"
                width="110"
              />
              <el-table-column
                prop="guestPort"
                :label="t('user.instanceDetail.internalPort')"
                width="110"
              />
              <el-table-column
                prop="protocol"
                :label="t('user.instanceDetail.protocol')"
                width="90"
              >
                <template #default="{ row }">
                  <el-tag
                    size="small"
                    :type="row.protocol === 'tcp' ? 'primary' : row.protocol === 'udp' ? 'success' : 'info'"
                  >
                    {{ row.protocol === 'both' ? 'TCP/UDP' : row.protocol.toUpperCase() }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                prop="status"
                :label="t('user.instanceDetail.status')"
                width="100"
              >
                <template #default="{ row }">
                  <el-tag
                    size="small"
                    :type="row.status === 'active' ? 'success' : 'info'"
                  >
                    {{ row.status === 'active' ? t('user.instanceDetail.active') : t('user.instanceDetail.unused') }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                :label="t('user.instanceDetail.connectionInfo')"
                min-width="300"
              >
                <template #default="{ row }">
                  <div class="connection-commands">
                    <!-- 控制端转发模式 -->
                    <div
                      v-if="row.mappingType === 'controller' && row.isSSH"
                      class="ssh-command"
                    >
                      <span 
                        class="command-text"
                        :title="t('user.instanceDetail.controllerSSHHint')"
                      >
                        {{ t('user.instanceDetail.controllerForwardingSSH', { port: row.hostPort }) }}
                      </span>
                      <el-tag
                        size="small"
                        type="warning"
                        style="margin-left: 8px;"
                      >
                        {{ t('user.instanceDetail.controllerForwarding') }}
                      </el-tag>
                    </div>
                    <!-- 控制端转发模式（非SSH端口） -->
                    <div
                      v-else-if="row.mappingType === 'controller'"
                      class="port-access"
                    >
                      <span 
                        class="command-text"
                        :title="t('user.instanceDetail.controllerPortHint', { port: row.hostPort })"
                      >
                        {{ t('user.instanceDetail.controllerForwardingPort', { port: row.hostPort }) }}
                      </span>
                      <el-tag
                        size="small"
                        type="warning"
                        style="margin-left: 8px;"
                      >
                        {{ t('user.instanceDetail.controllerForwarding') }}
                      </el-tag>
                    </div>
                    <!-- 节点侧映射（SSH端口） -->
                    <div
                      v-else-if="row.isSSH"
                      class="ssh-command"
                    >
                      <span 
                        class="command-text" 
                        :title="`ssh ${instance.username || 'root'}@${instance.publicIP} -p ${row.hostPort}`"
                      >
                        {{ formatSSHCommand(instance.username, instance.publicIP, row.hostPort) }}
                      </span>
                      <el-button 
                        size="small" 
                        text 
                        @click="copyToClipboard(`ssh ${instance.username || 'root'}@${instance.publicIP} -p ${row.hostPort}`)"
                      >
                        {{ t('user.instanceDetail.copy') }}
                      </el-button>
                    </div>
                    <!-- 节点侧映射（非SSH端口） -->
                    <div
                      v-else
                      class="port-access"
                    >
                      <span 
                        class="command-text" 
                        :title="`${instance.publicIP}:${row.hostPort}`"
                      >
                        {{ formatIPPort(instance.publicIP, row.hostPort) }}
                      </span>
                      <el-button 
                        size="small" 
                        text 
                        @click="copyToClipboard(`${instance.publicIP}:${row.hostPort}`)"
                      >
                        {{ t('user.instanceDetail.copy') }}
                      </el-button>
                    </div>
                  </div>
                </template>
              </el-table-column>
            </el-table>
            
            <div 
              v-else
              class="no-ports"
            >
              <p>{{ t('user.instances.portMapping') }}</p>
            </div>
          </div>
        </el-tab-pane>

        <!-- 统计标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.statistics')"
          name="stats"
        >
          <div class="stats-content">
            <!-- 流量统计 -->
            <div class="traffic-section">
              <div class="traffic-stats">
                <div class="traffic-usage">
                  <div class="usage-header">
                    <span class="usage-label">{{ t('user.trafficOverview.currentMonthUsage') }}</span>
                    <span class="usage-info">
                      {{ formatTraffic(monitoring.trafficData?.currentMonth || 0) }} / 
                      {{ formatTraffic(monitoring.trafficData?.totalLimit || 102400) }}
                    </span>
                  </div>
                  <div class="usage-details">
                    <span :class="{ 'limited-text': monitoring.trafficData?.isLimited }">
                      {{ monitoring.trafficData?.isLimited ? t('user.instanceDetail.trafficOverlimit') : t('user.instanceDetail.normalUsage') }}
                    </span>
                    <span class="reset-info">{{ t('user.trafficOverview.resetOn1st') }}</span>
                  </div>
                </div>

                <!-- 流量超限警告 -->
                <el-alert
                  v-if="monitoring?.trafficData?.isLimited"
                  :title="getTrafficLimitTitle()"
                  :description="monitoring.trafficData.limitReason"
                  :type="getTrafficLimitType()"
                  :closable="false"
                  show-icon
                  style="margin: 20px 0;"
                />
                
                <div
                  v-if="monitoring.trafficData?.history?.length"
                  class="traffic-breakdown"
                >
                  <h4>{{ t('user.trafficOverview.historicalStats') }}</h4>
                  <div class="history-list">
                    <div 
                      v-for="item in monitoring.trafficData.history.slice(0, 6)" 
                      :key="`${item.year}-${item.month}`"
                      class="history-item"
                    >
                      <span class="month">{{ item.year }}-{{ String(item.month).padStart(2, '0') }}</span>
                      <span class="traffic">{{ formatTraffic(item.totalUsed) }}</span>
                      <span class="breakdown">
                        ↑{{ formatTraffic(item.trafficOut) }} ↓{{ formatTraffic(item.trafficIn) }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <!-- 流量历史趋势图 -->
            <TrafficHistoryChart
              ref="trafficChartRef"
              type="instance"
              :resource-id="route.params.id"
              :title="''"
              :auto-refresh="0"
            >
              <template #extra-actions>
                <el-button
                  size="small"
                  @click="refreshMonitoring"
                >
                  <el-icon><Refresh /></el-icon>
                  {{ t('common.refresh') }}
                </el-button>
                <el-button
                  size="small"
                  type="primary"
                  @click="showTrafficDetail = true"
                >
                  {{ t('user.trafficOverview.viewDetailedStats') }}
                </el-button>
              </template>
            </TrafficHistoryChart>
          </div>
        </el-tab-pane>

        <!-- 资源监控标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.resourceMonitoring')"
          name="resources"
        >
          <ResourceMonitorChart
            ref="resourceChartRef"
            :instance-id="route.params.id"
            :auto-refresh="300000"
          />
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- PMAcct 流量详情对话框 -->
    <InstanceTrafficDetail
      v-model="showTrafficDetail"
      :instance-id="route.params.id"
      :instance-name="instance.name"
    />

    <!-- 重置系统镜像选择对话框 -->
    <el-dialog
      v-model="showResetImageDialog"
      :title="t('user.instanceDetail.selectResetImage')"
      width="500px"
      destroy-on-close
    >
      <div v-loading="loadingResetImages">
        <p style="margin-bottom: 12px; color: var(--el-text-color-secondary);">
          {{ t('user.instanceDetail.selectResetImageTip') }}
        </p>
        <el-radio-group
          v-model="selectedResetImage"
          style="display: flex; flex-direction: column; gap: 8px;"
        >
          <el-radio
            v-for="img in resetImages"
            :key="img.name || img.id"
            :value="img.name"
            border
            style="margin: 0; width: 100%;"
          >
            <span style="display: inline-flex; align-items: center; gap: 6px;">
              <OsIcon
                :name="img.name"
                :size="20"
              />
              {{ img.display_name || img.name }}
            </span>
          </el-radio>
        </el-radio-group>
      </div>
      <template #footer>
        <el-button @click="showResetImageDialog = false">
          {{ t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :disabled="!selectedResetImage"
          @click="confirmResetWithImage"
        >
          {{ t('user.instanceDetail.confirmReset') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { formatDiskSize, formatMemorySize } from '@/utils/unit-formatter'
import InstanceTrafficDetail from '@/components/InstanceTrafficDetail.vue'
import TrafficHistoryChart from '@/components/TrafficHistoryChart.vue'
import ResourceMonitorChart from '@/components/ResourceMonitorChart.vue'
import {
  ArrowLeft,
  VideoPlay,
  VideoPause,
  Refresh,
  Delete,
  Monitor
} from '@element-plus/icons-vue'
import { useInstanceDetail } from './composables/useInstanceDetail'
import { useInstanceActions } from './composables/useInstanceActions'
import { useInstanceFormatters } from './composables/useInstanceFormatters'
import OsIcon from '@/components/OsIcon.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const activeTab = ref('overview')
const resourceChartRef = ref(null)

const {
  loading,
  portMappings,
  trafficChartRef,
  instanceTypePermissions,
  instance,
  monitoring,
  updateInstancePermissions,
  loadInstanceDetail,
  refreshPortMappings,
  refreshMonitoring,
  loadInstanceTypePermissions
} = useInstanceDetail()

const {
  actionLoading,
  showPassword,
  showTrafficDetail,
  showResetImageDialog,
  resetImages,
  selectedResetImage,
  loadingResetImages,
  confirmResetWithImage,
  viewTaskDetail,
  performAction,
  openSSHTerminal,
  openExecTerminal,
  showResetPasswordDialog,
  togglePassword,
  truncateIP,
  formatSSHCommand,
  formatIPPort,
  copyToClipboard
} = useInstanceActions(instance, monitoring, loadInstanceDetail)

const {
  getNetworkTypeFromLegacy,
  getNetworkTypeDisplayName,
  getNetworkTypeTagType,
  getProviderTypeName,
  getProviderTypeColor,
  getTaskTitle,
  getTaskTypeText,
  getTaskAlertType,
  getStatusType,
  getStatusText,
  getTrafficProgressColor,
  formatTraffic,
  formatDate,
  getTrafficLimitTitle: _getTrafficLimitTitle,
  getTrafficLimitType: _getTrafficLimitType
} = useInstanceFormatters()

// wrap monitoring-dependent helpers
const getTrafficLimitTitle = () => _getTrafficLimitTitle(monitoring)
const getTrafficLimitType = () => _getTrafficLimitType(monitoring)

// 标志位，防止 watch 循环触发
let isUpdatingFromRoute = false

watch(() => route.params.id, async (newId, oldId) => {
  if (newId && newId !== oldId && newId !== 'undefined') {
    try {
      const [detailSuccess, permissionsSuccess] = await Promise.all([
        loadInstanceDetail(true),
        loadInstanceTypePermissions()
      ])
      if (detailSuccess && permissionsSuccess) {
        updateInstancePermissions()
        refreshMonitoring()
        refreshPortMappings()
      }
    } catch (error) {
      console.error('路由切换时加载数据失败:', error)
    }
  }
})

watch(() => route.query.tab, (newTab, oldTab) => {
  if (newTab === oldTab) return
  if (newTab && ['overview', 'ports', 'stats', 'resources'].includes(newTab)) {
    if (activeTab.value === newTab) return
    isUpdatingFromRoute = true
    activeTab.value = newTab
    nextTick(() => { isUpdatingFromRoute = false })
  } else {
    if (activeTab.value !== 'overview') {
      isUpdatingFromRoute = true
      activeTab.value = 'overview'
      nextTick(() => { isUpdatingFromRoute = false })
    }
  }
}, { immediate: true })

watch(activeTab, (newTab, oldTab) => {
  if (newTab === oldTab || isUpdatingFromRoute) return
  if (newTab && route.query.tab !== newTab) {
    router.replace({ query: { ...route.query, tab: newTab } })
  }
})

let monitoringTimer = null

onMounted(async () => {
  await nextTick()
  try {
    const [detailSuccess, permissionsSuccess] = await Promise.all([
      loadInstanceDetail(true),
      loadInstanceTypePermissions()
    ])
    if (detailSuccess && permissionsSuccess) {
      updateInstancePermissions()
      refreshMonitoring()
      refreshPortMappings()
      monitoringTimer = setInterval(refreshMonitoring, 30000)
    }
  } catch (error) {
    console.error('页面初始化失败:', error)
  }
})

onUnmounted(() => {
  if (monitoringTimer) {
    clearInterval(monitoringTimer)
    monitoringTimer = null
  }
})
</script>

<style src="./detail.css" scoped></style>

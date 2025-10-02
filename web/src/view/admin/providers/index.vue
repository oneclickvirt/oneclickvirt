<template>
  <div class="providers-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>服务器提供商管理</span>
          <el-button
            type="primary"
            @click="showAddDialog = true"
          >
            添加服务器
          </el-button>
        </div>
      </template>
      
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
          label="名称"
        />
        <el-table-column
          prop="type"
          label="类型"
        />
        <el-table-column
          label="位置"
          width="120"
        >
          <template #default="scope">
            <div class="location-cell">
              <span
                v-if="scope.row.countryCode"
                class="flag-icon"
              >{{ getFlagEmoji(scope.row.countryCode) }}</span>
              <span>{{ scope.row.country || scope.row.region || '-' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          label="主机地址"
          width="140"
        >
          <template #default="scope">
            {{ scope.row.endpoint ? scope.row.endpoint.split(':')[0] : '-' }}
          </template>
        </el-table-column>
        <el-table-column
          label="SSH端口"
          width="80"
        >
          <template #default="scope">
            {{ scope.row.sshPort || 22 }}
          </template>
        </el-table-column>
        <el-table-column
          label="支持类型"
          width="120"
        >
          <template #default="scope">
            <div class="support-types">
              <el-tag
                v-if="scope.row.container_enabled"
                size="small"
                type="primary"
              >
                容器
              </el-tag>
              <el-tag
                v-if="scope.row.vm_enabled"
                size="small"
                type="success"
              >
                虚拟机
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="architecture"
          label="架构"
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
          label="存储池"
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
              未配置
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
          label="连接状态"
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
          label="过期时间"
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
              永不过期
            </el-text>
          </template>
        </el-table-column>
        <el-table-column
          label="流量使用"
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
                  已超限
                </el-tag>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          label="服务器状态"
          width="100"
        >
          <template #default="scope">
            <el-tag
              v-if="scope.row.isFrozen"
              type="danger"
              size="small"
            >
              已冻结
            </el-tag>
            <el-tag
              v-else-if="isExpired(scope.row.expiresAt)"
              type="warning"
              size="small"
            >
              已过期
            </el-tag>
            <el-tag
              v-else
              type="success"
              size="small"
            >
              正常
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          label="节点资源"
          width="120"
        >
          <template #default="scope">
            <div
              v-if="scope.row.resourceSynced"
              class="resource-info"
            >
              <div class="resource-item">
                <el-icon><Cpu /></el-icon>
                <span>{{ scope.row.nodeCpuCores || 0 }} 核</span>
              </div>
              <div class="resource-item">
                <el-icon><Monitor /></el-icon>
                <span>{{ formatMemorySize(scope.row.nodeMemoryTotal) }}</span>
                <span v-if="scope.row.nodeSwapTotal > 0">+{{ formatMemorySize(scope.row.nodeSwapTotal) }}S</span>
              </div>
              <div class="resource-item">
                <el-icon><FolderOpened /></el-icon>
                <span>{{ formatDiskSize(scope.row.nodeDiskTotal) }} 总空间</span>
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
                未同步
              </el-text>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          label="任务状态"
          width="120"
        >
          <template #default="scope">
            <div class="task-status">
              <div style="margin-bottom: 4px;">
                <el-text size="small">
                  实例: {{ scope.row.instanceCount || 0 }}
                </el-text>
              </div>
              <div style="margin-bottom: 4px;">
                <el-text size="small">
                  运行任务: {{ scope.row.runningTasksCount || 0 }}
                </el-text>
              </div>
              <div>
                <el-tag
                  v-if="scope.row.allowConcurrentTasks"
                  type="success"
                  size="small"
                >
                  并发 ({{ scope.row.maxConcurrentTasks }})
                </el-tag>
                <el-tag
                  v-else
                  type="warning"
                  size="small"
                >
                  串行
                </el-tag>
              </div>
              <div style="margin-top: 4px;">
                <el-tag
                  v-if="scope.row.enableTaskPolling"
                  type="primary"
                  size="small"
                >
                  轮询 {{ scope.row.taskPollInterval }}s
                </el-tag>
                <el-tag
                  v-else
                  type="info"
                  size="small"
                >
                  已禁用轮询
                </el-tag>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          label="操作"
          width="290"
          fixed="right"
        >
          <template #default="scope">
            <div class="table-action-buttons">
              <a
                class="table-action-link"
                @click="editProvider(scope.row)"
              >
                编辑
              </a>
              <a 
                v-if="(scope.row.type === 'lxd' || scope.row.type === 'incus' || scope.row.type === 'proxmox')" 
                class="table-action-link" 
                @click="autoConfigureAPI(scope.row)"
              >
                自动配置API
              </a>
              <a 
                class="table-action-link" 
                @click="checkHealth(scope.row.id)"
              >
                健康检查
              </a>
              <a 
                v-if="scope.row.isFrozen" 
                class="table-action-link success" 
                @click="unfreezeServer(scope.row)"
              >
                解冻
              </a>
              <a 
                v-else 
                class="table-action-link warning" 
                @click="freezeServer(scope.row.id)"
              >
                冻结
              </a>
              <a
                class="table-action-link danger"
                @click="handleDeleteProvider(scope.row.id)"
              >
                删除
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
      :title="isEditing ? '编辑服务器' : '添加服务器'" 
      width="800px"
      :close-on-click-modal="false"
    >
      <!-- 配置分类标签页 -->
      <el-tabs
        v-model="activeConfigTab"
        type="border-card"
        class="server-config-tabs"
      >
        <!-- 基本信息 -->
        <el-tab-pane
          label="基本信息"
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
              label="服务器名称"
              prop="name"
            >
              <el-input
                v-model="addProviderForm.name"
                placeholder="请输入服务器名称"
              />
            </el-form-item>
            <el-form-item
              label="服务器类型"
              prop="type"
            >
              <el-select
                v-model="addProviderForm.type"
                placeholder="请选择服务器类型"
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
              label="主机地址"
              prop="host"
            >
              <el-input
                v-model="addProviderForm.host"
                placeholder="请输入主机IP或域名"
              />
            </el-form-item>
            <el-form-item
              label="端口"
              prop="port"
            >
              <el-input-number
                v-model="addProviderForm.port"
                :min="1"
                :max="65535"
              />
            </el-form-item>
            <el-form-item
              label="描述"
              prop="description"
            >
              <el-input 
                v-model="addProviderForm.description" 
                type="textarea" 
                :rows="3"
                placeholder="服务器描述信息"
              />
            </el-form-item>
            <el-form-item
              label="状态"
              prop="status"
            >
              <el-select
                v-model="addProviderForm.status"
                placeholder="请选择状态"
              >
                <el-option
                  label="启用"
                  value="active"
                />
                <el-option
                  label="禁用"
                  value="inactive"
                />
              </el-select>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 连接配置 -->
        <el-tab-pane
          label="连接配置"
          name="connection"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-form-item
              label="用户名"
              prop="username"
            >
              <el-input
                v-model="addProviderForm.username"
                placeholder="请输入连接用户名"
              />
            </el-form-item>
            <el-form-item
              label="密码"
              prop="password"
            >
              <el-input 
                v-model="addProviderForm.password" 
                type="password" 
                :placeholder="isEditing ? '不修改请留空' : '请输入连接密码'" 
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
                  留空表示不修改当前密码
                </el-text>
              </div>
            </el-form-item>
            <el-form-item
              label="SSH密钥"
              prop="sshKey"
            >
              <el-input 
                v-model="addProviderForm.sshKey" 
                type="textarea" 
                :rows="4"
                placeholder="可选：SSH私钥内容"
              />
            </el-form-item>
            
            <el-divider content-position="left">
              SSH超时配置
            </el-divider>
            
            <el-form-item
              label="连接超时"
              prop="sshConnectTimeout"
            >
              <el-input-number
                v-model="addProviderForm.sshConnectTimeout"
                :min="5"
                :max="300"
                :step="5"
                placeholder="30"
              />
              <span style="margin-left: 10px;">秒</span>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  SSH连接建立的超时时间，Docker环境建议设置较大值（30秒以上）
                </el-text>
              </div>
            </el-form-item>
            
            <el-form-item
              label="执行超时"
              prop="sshExecuteTimeout"
            >
              <el-input-number
                v-model="addProviderForm.sshExecuteTimeout"
                :min="30"
                :max="3600"
                :step="30"
                placeholder="300"
              />
              <span style="margin-left: 10px;">秒</span>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  SSH命令执行的超时时间，复杂操作建议设置300秒以上
                </el-text>
              </div>
            </el-form-item>
            
            <el-form-item label="连接测试">
              <el-button
                type="primary"
                :loading="testingConnection"
                :disabled="!addProviderForm.host || !addProviderForm.username || !addProviderForm.password"
                @click="testSSHConnection"
              >
                <el-icon v-if="!testingConnection">
                  <Connection />
                </el-icon>
                {{ testingConnection ? '测试中...' : '测试SSH连接' }}
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
                      <p><strong>测试结果：</strong></p>
                      <p>最小延迟: {{ connectionTestResult.minLatency }}ms</p>
                      <p>最大延迟: {{ connectionTestResult.maxLatency }}ms</p>
                      <p>平均延迟: {{ connectionTestResult.avgLatency }}ms</p>
                      <p style="margin-top: 8px;">
                        <strong>推荐超时时间: {{ connectionTestResult.recommendedTimeout }}秒</strong>
                      </p>
                      <el-button
                        type="primary"
                        size="small"
                        style="margin-top: 8px;"
                        @click="applyRecommendedTimeout"
                      >
                        应用推荐值
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
          label="地理位置"
          name="location"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-form-item
              label="地区"
              prop="region"
            >
              <el-input
                v-model="addProviderForm.region"
                placeholder="请输入地区，如：华东、美西等"
              />
            </el-form-item>
            <el-form-item
              label="国家"
              prop="country"
            >
              <el-select 
                v-model="addProviderForm.country" 
                placeholder="请选择国家"
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
                  选择服务器所在国家，中国地区将使用CDN加速下载GitHub镜像
                </el-text>
              </div>
            </el-form-item>
            <el-form-item
              label="架构"
              prop="architecture"
            >
              <el-select
                v-model="addProviderForm.architecture"
                placeholder="请选择服务器架构"
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
                  选择服务器的CPU架构，影响可用的系统镜像
                </el-text>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 虚拟化配置 -->
        <el-tab-pane
          label="虚拟化配置"
          name="virtualization"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-alert
              title="虚拟化类型说明"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              配置该服务器支持的虚拟化类型和实例数量限制
            </el-alert>
            
            <el-form-item
              label="支持类型"
              prop="supportTypes"
            >
              <div class="support-type-group">
                <el-checkbox v-model="addProviderForm.containerEnabled">
                  <span>支持容器</span>
                  <el-tooltip
                    content="支持Docker、LXC等容器技术"
                    placement="top"
                  >
                    <el-icon style="margin-left: 5px;">
                      <InfoFilled />
                    </el-icon>
                  </el-tooltip>
                </el-checkbox>
                <el-checkbox 
                  v-model="addProviderForm.vmEnabled"
                  :disabled="addProviderForm.type === 'docker'"
                >
                  <span>支持虚拟机</span>
                  <el-tooltip
                    content="支持KVM、Xen等虚拟化技术"
                    placement="top"
                  >
                    <el-icon style="margin-left: 5px;">
                      <InfoFilled />
                    </el-icon>
                  </el-tooltip>
                </el-checkbox>
              </div>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ addProviderForm.type === 'docker' ? 'Docker只支持容器虚拟化' : '至少选择一种支持的虚拟化类型' }}
                </el-text>
              </div>
            </el-form-item>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item
                  label="最大容器数"
                  prop="maxContainerInstances"
                >
                  <el-input-number
                    v-model="addProviderForm.maxContainerInstances"
                    :min="0"
                    :max="1000"
                    :step="1"
                    placeholder="0表示无限制"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      设置最大容器实例数量，0表示无限制
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  label="最大虚拟机数"
                  prop="maxVMInstances"
                >
                  <el-input-number
                    v-model="addProviderForm.maxVMInstances"
                    :min="0"
                    :max="1000"
                    :step="1"
                    placeholder="0表示无限制"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      设置最大虚拟机实例数量，0表示无限制
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <!-- ProxmoxVE存储配置 -->
            <el-form-item
              v-if="addProviderForm.type === 'proxmox'"
              label="存储池"
              prop="storagePool"
            >
              <el-input
                v-model="addProviderForm.storagePool"
                placeholder="请输入存储池名称"
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
                  ProxmoxVE存储池名称，用于存储虚拟机磁盘和容器，如：local、local-lvm、nfs-storage等
                </el-text>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- IP映射配置 -->
        <el-tab-pane
          label="IP映射配置"
          name="mapping"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-alert
              title="端口映射配置说明"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              配置该服务器的端口映射策略和IP地址分配方式，决定实例如何对外提供服务。
            </el-alert>

            <el-form-item
              label="默认端口数量"
              prop="defaultPortCount"
            >
              <el-input-number
                v-model="addProviderForm.defaultPortCount"
                :min="1"
                :max="50"
                :step="1"
                placeholder="10"
                style="width: 200px"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  每个实例默认分配的端口映射数量（包括SSH端口）
                </el-text>
              </div>
            </el-form-item>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item
                  label="端口范围起始"
                  prop="portRangeStart"
                >
                  <el-input-number
                    v-model="addProviderForm.portRangeStart"
                    :min="1024"
                    :max="65535"
                    :step="1"
                    placeholder="10000"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      端口映射范围的起始端口号
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  label="端口范围结束"
                  prop="portRangeEnd"
                >
                  <el-input-number
                    v-model="addProviderForm.portRangeEnd"
                    :min="1024"
                    :max="65535"
                    :step="1"
                    placeholder="65535"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      端口映射范围的结束端口号
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item
              label="网络配置类型"
              prop="networkType"
            >
              <el-select
                v-model="addProviderForm.networkType"
                placeholder="请选择网络配置类型"
                style="width: 100%"
              >
                <el-option
                  label="NAT IPv4"
                  value="nat_ipv4"
                />
                <el-option
                  label="NAT IPv4 + 独立IPv6"
                  value="nat_ipv4_ipv6"
                />
                <el-option
                  label="独立IPv4"
                  value="dedicated_ipv4"
                />
                <el-option
                  label="独立IPv4 + 独立IPv6"
                  value="dedicated_ipv4_ipv6"
                />
                <el-option
                  label="纯IPv6"
                  value="ipv6_only"
                />
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  NAT IPv4：多个实例共享公网IPv4；独立IPv4：每个实例独立IPv4；纯IPv6：仅配置IPv6地址
                </el-text>
              </div>
            </el-form-item>

            <!-- IPv4端口映射方式 -->
            <el-form-item
              v-if="(addProviderForm.type === 'lxd' || addProviderForm.type === 'incus') && addProviderForm.networkType !== 'ipv6_only'"
              label="IPv4端口映射方式"
              prop="ipv4PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv4PortMappingMethod"
                placeholder="请选择IPv4端口映射方式"
                style="width: 100%"
              >
                <el-option
                  label="Device Proxy（推荐）"
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
                  IPv4端口映射的实现方式，device_proxy性能更好但需要较新版本的LXD/Incus
                </el-text>
              </div>
            </el-form-item>

            <!-- IPv6端口映射方式 -->
            <el-form-item
              v-if="(addProviderForm.type === 'lxd' || addProviderForm.type === 'incus') && (addProviderForm.networkType === 'nat_ipv4_ipv6' || addProviderForm.networkType === 'dedicated_ipv4_ipv6' || addProviderForm.networkType === 'ipv6_only')"
              label="IPv6端口映射方式"
              prop="ipv6PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv6PortMappingMethod"
                placeholder="请选择IPv6端口映射方式"
                style="width: 100%"
              >
                <el-option
                  label="Device Proxy（推荐）"
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
                  IPv6端口映射的实现方式，device_proxy性能更好但需要较新版本的LXD/Incus
                </el-text>
              </div>
            </el-form-item>

            <!-- Proxmox IPv4端口映射方式 -->
            <el-form-item
              v-if="addProviderForm.type === 'proxmox' && addProviderForm.networkType !== 'ipv6_only'"
              label="IPv4端口映射方式"
              prop="ipv4PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv4PortMappingMethod"
                placeholder="请选择IPv4端口映射方式"
                style="width: 100%"
              >
                <el-option
                  v-if="addProviderForm.networkType === 'dedicated_ipv4' || addProviderForm.networkType === 'dedicated_ipv4_ipv6'"
                  label="原生实现（推荐）"
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
                  NAT模式下只能使用iptables，独立IP模式下推荐使用原生实现
                </el-text>
              </div>
            </el-form-item>

            <!-- Proxmox IPv6端口映射方式 -->
            <el-form-item
              v-if="addProviderForm.type === 'proxmox' && (addProviderForm.networkType === 'nat_ipv4_ipv6' || addProviderForm.networkType === 'dedicated_ipv4_ipv6' || addProviderForm.networkType === 'ipv6_only')"
              label="IPv6端口映射方式"
              prop="ipv6PortMappingMethod"
            >
              <el-select
                v-model="addProviderForm.ipv6PortMappingMethod"
                placeholder="请选择IPv6端口映射方式"
                style="width: 100%"
              >
                <el-option
                  label="原生实现（推荐）"
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
                  IPv6默认使用原生实现，性能更好
                </el-text>
              </div>
            </el-form-item>

            <el-alert
              title="映射类型说明"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 20px;"
            >
              <ul style="margin: 0; padding-left: 20px;">
                <li><strong>NAT映射：</strong>多个实例共享宿主机的IPv4地址，通过不同端口访问</li>
                <li><strong>独立映射：</strong>每个实例分配独立公网IPv4地址</li>
                <li><strong>IPv6支持：</strong>开设的实例自动分配独立的IPv6地址</li>
                <li><strong>Docker：</strong>IPv4/IPv6都使用原生实现，不可选择</li>
                <li><strong>LXD/Incus：</strong>IPv4默认Device Proxy；IPv6默认Device Proxy，可选Iptables</li>
                <li><strong>Proxmox VE：</strong>IPv4 NAT模式默认Iptables，独立IP模式默认原生实现；IPv6默认原生实现</li>
              </ul>
            </el-alert>
          </el-form>
        </el-tab-pane>

        <!-- 带宽配置 -->
        <el-tab-pane
          label="带宽配置"
          name="bandwidth"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-alert
              title="带宽配置说明"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              配置该服务器的默认带宽限制，实例创建时将根据用户等级和这些配置自动应用合适的带宽限制。
            </el-alert>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item
                  label="默认入站带宽"
                  prop="defaultInboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.defaultInboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    placeholder="300"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      默认入站带宽限制（Mbps），实例创建时的默认值
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  label="默认出站带宽"
                  prop="defaultOutboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.defaultOutboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    placeholder="300"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      默认出站带宽限制（Mbps），实例创建时的默认值
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-row :gutter="20">
              <el-col :span="12">
                <el-form-item
                  label="最大入站带宽"
                  prop="maxInboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.maxInboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    placeholder="1000"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      最大入站带宽限制（Mbps），实例不能超过此值
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item
                  label="最大出站带宽"
                  prop="maxOutboundBandwidth"
                >
                  <el-input-number
                    v-model="addProviderForm.maxOutboundBandwidth"
                    :min="1"
                    :max="10000"
                    :step="50"
                    placeholder="1000"
                    style="width: 100%"
                  />
                  <div class="form-tip">
                    <el-text
                      size="small"
                      type="info"
                    >
                      最大出站带宽限制（Mbps），实例不能超过此值
                    </el-text>
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">流量配置</span>
            </el-divider>

            <el-form-item
              label="最大流量限制"
              prop="maxTraffic"
            >
              <el-input-number
                v-model="maxTrafficTB"
                :min="0.001"
                :max="10"
                :step="0.1"
                :precision="3"
                placeholder="1"
                style="width: 100%"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  最大流量限制（TB），支持小数点，默认1TB，每月1号自动重置
                </el-text>
              </div>
            </el-form-item>

            <el-alert
              title="带宽限制机制说明"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 20px;"
            >
              <ul style="margin: 0; padding-left: 20px;">
                <li><strong>默认带宽：</strong>实例创建时的初始带宽限制，会结合用户等级进行调整</li>
                <li><strong>最大带宽：</strong>该服务器上任何实例都不能超过的带宽上限</li>
                <li><strong>用户等级：</strong>最终带宽 = min(用户等级限制, 默认带宽, 最大带宽)</li>
                <li><strong>流量限制：</strong>每月1号自动重置，超过限制后Provider将被限流</li>
              </ul>
            </el-alert>
          </el-form>
        </el-tab-pane>

        <!-- 高级设置 -->
        <el-tab-pane
          label="高级设置"
          name="advanced"
        >
          <el-form
            :model="addProviderForm"
            label-width="120px"
            class="server-form"
          >
            <el-form-item
              label="过期时间"
              prop="expiresAt"
            >
              <el-date-picker
                v-model="addProviderForm.expiresAt"
                type="datetime"
                placeholder="请选择过期时间"
                format="YYYY-MM-DD HH:mm:ss"
                value-format="YYYY-MM-DD HH:mm:ss"
                :disabled-date="(time) => time.getTime() < Date.now() - 8.64e7"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  不设置则默认31天后过期，过期后服务器将被冻结
                </el-text>
              </div>
            </el-form-item>

            <!-- 并发控制设置 -->
            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">任务并发控制</span>
            </el-divider>
            
            <el-form-item
              label="允许并发任务"
              prop="allowConcurrentTasks"
            >
              <el-switch
                v-model="addProviderForm.allowConcurrentTasks"
                active-text="是"
                inactive-text="否"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  是否允许在同一个Provider上并发执行多个任务（创建、删除、操作实例等）
                </el-text>
              </div>
            </el-form-item>

            <el-form-item
              v-if="addProviderForm.allowConcurrentTasks"
              label="最大并发任务数"
              prop="maxConcurrentTasks"
            >
              <el-input-number
                v-model="addProviderForm.maxConcurrentTasks"
                :min="1"
                :max="10"
                :step="1"
                placeholder="1"
                style="width: 200px"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  同时执行的任务数量上限，建议根据服务器性能设置
                </el-text>
              </div>
            </el-form-item>

            <!-- 任务轮询设置 -->
            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">任务轮询设置</span>
            </el-divider>
            
            <el-form-item
              label="启用任务轮询"
              prop="enableTaskPolling"
            >
              <el-switch
                v-model="addProviderForm.enableTaskPolling"
                active-text="是"
                inactive-text="否"
              />
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  是否启用该Provider的任务轮询检测，关闭后任务将不会自动执行
                </el-text>
              </div>
            </el-form-item>

            <el-form-item
              v-if="addProviderForm.enableTaskPolling"
              label="轮询间隔"
              prop="taskPollInterval"
            >
              <el-input-number
                v-model="addProviderForm.taskPollInterval"
                :min="5"
                :max="300"
                :step="5"
                placeholder="60"
                style="width: 200px"
              />
              <span style="margin-left: 10px; color: #666;">秒</span>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  检测待处理任务的间隔时间，范围 5-300 秒，默认60秒，频繁轮询会增加系统负载
                </el-text>
              </div>
            </el-form-item>

            <!-- 操作执行规则设置 -->
            <el-divider content-position="left">
              <span style="color: #666; font-size: 14px;">操作执行规则</span>
            </el-divider>
            
            <el-form-item
              label="执行规则"
              prop="executionRule"
            >
              <el-select
                v-model="addProviderForm.executionRule"
                placeholder="请选择操作轮转规则"
                style="width: 200px"
              >
                <el-option
                  label="自动切换"
                  value="auto"
                >
                  <span>自动切换</span>
                  <span style="float: right; color: #8492a6; font-size: 12px;">API不可用时自动切换SSH</span>
                </el-option>
                <el-option
                  label="仅API执行"
                  value="api_only"
                >
                  <span>仅API执行</span>
                  <span style="float: right; color: #8492a6; font-size: 12px;">只使用API接口</span>
                </el-option>
                <el-option
                  label="仅SSH执行"
                  value="ssh_only"
                >
                  <span>仅SSH执行</span>
                  <span style="float: right; color: #8492a6; font-size: 12px;">只使用SSH连接</span>
                </el-option>
              </el-select>
              <div class="form-tip">
                <el-text
                  size="small"
                  type="info"
                >
                  选择Provider执行任务和操作的方式（除健康检测外）。自动切换：API不可用时自动使用SSH；仅API：只使用API接口；仅SSH：只使用SSH连接
                </el-text>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>
      
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelAddServer">取消</el-button>
          <el-button
            type="primary"
            :loading="addProviderLoading"
            @click="submitAddServer"
          >确定</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 自动配置结果对话框 -->
    <el-dialog 
      v-model="configDialog.visible" 
      title="API 自动配置" 
      width="900px"
      :close-on-click-modal="false"
      :close-on-press-escape="false"
    >
      <div v-if="configDialog.provider">
        <!-- 历史记录视图 -->
        <div v-if="configDialog.showHistory">
          <el-alert
            :title="`${configDialog.provider.type.toUpperCase()} 配置历史`"
            type="info"
            :closable="false"
            show-icon
            style="margin-bottom: 20px;"
          >
            <template #default>
              <p>检测到该Provider的配置历史记录，请选择操作：</p>
            </template>
          </el-alert>

          <!-- 正在运行的任务 -->
          <div
            v-if="configDialog.runningTask"
            style="margin-bottom: 20px;"
          >
            <el-alert
              title="有正在运行的配置任务"
              type="warning"
              :closable="false"
              show-icon
            >
              <template #default>
                <p>任务ID: {{ configDialog.runningTask.id }}</p>
                <p>开始时间: {{ new Date(configDialog.runningTask.startedAt).toLocaleString() }}</p>
                <p>执行者: {{ configDialog.runningTask.executorName }}</p>
              </template>
            </el-alert>
          </div>

          <!-- 历史任务列表 -->
          <div v-if="configDialog.historyTasks.length > 0">
            <h4>配置历史记录</h4>
            <el-table
              :data="configDialog.historyTasks"
              size="small"
              style="margin-bottom: 20px;"
            >
              <el-table-column
                prop="id"
                label="任务ID"
                width="70"
              />
              <el-table-column
                label="状态"
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
                label="执行时间"
                width="140"
              >
                <template #default="{ row }">
                  {{ new Date(row.createdAt).toLocaleString() }}
                </template>
              </el-table-column>
              <el-table-column
                prop="executorName"
                label="执行者"
                width="80"
              />
              <el-table-column
                prop="duration"
                label="耗时"
                width="70"
              />
              <el-table-column
                label="结果"
                show-overflow-tooltip
              >
                <template #default="{ row }">
                  <span
                    v-if="row.success"
                    style="color: #67C23A;"
                  >✅ 成功</span>
                  <span
                    v-else-if="row.status === 'failed'"
                    style="color: #F56C6C;"
                  >❌ {{ row.errorMessage || '失败' }}</span>
                  <span v-else>{{ row.logSummary || '-' }}</span>
                </template>
              </el-table-column>
              <el-table-column
                label="操作"
                width="100"
              >
                <template #default="{ row }">
                  <el-button 
                    type="primary" 
                    size="small"
                    @click="viewTaskLog(row.id)"
                  >
                    查看日志
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
              查看运行中任务日志
            </el-button>
            <el-button 
              type="warning"
              @click="rerunConfiguration"
            >
              重新执行配置
            </el-button>
            <el-button @click="configDialog.visible = false">
              关闭
            </el-button>
          </div>
        </div>
      </div>
    </el-dialog>

    <!-- 任务日志查看对话框 -->
    <el-dialog
      v-model="taskLogDialog.visible"
      title="任务执行日志"
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
          正在加载任务日志...
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
            <span>任务信息</span>
          </template>
          <el-descriptions
            :column="2"
            border
          >
            <el-descriptions-item label="任务ID">
              {{ taskLogDialog.task.id }}
            </el-descriptions-item>
            <el-descriptions-item label="Provider">
              {{ taskLogDialog.task.providerName }}
            </el-descriptions-item>
            <el-descriptions-item label="任务类型">
              {{ taskLogDialog.task.taskType }}
            </el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="getTaskStatusType(taskLogDialog.task.status)">
                {{ getTaskStatusText(taskLogDialog.task.status) }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="执行者">
              {{ taskLogDialog.task.executorName }}
            </el-descriptions-item>
            <el-descriptions-item label="执行时长">
              {{ taskLogDialog.task.duration }}
            </el-descriptions-item>
            <el-descriptions-item label="开始时间">
              {{ taskLogDialog.task.startedAt ? new Date(taskLogDialog.task.startedAt).toLocaleString() : '-' }}
            </el-descriptions-item>
            <el-descriptions-item label="完成时间">
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
              <span>执行日志</span>
              <el-button 
                v-if="taskLogDialog.task && taskLogDialog.task.logOutput" 
                size="small"
                @click="copyTaskLog"
              >
                复制日志
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
import { InfoFilled, DocumentCopy, Loading, Cpu, Monitor, FolderOpened } from '@element-plus/icons-vue'
import { getProviderList, createProvider, updateProvider, deleteProvider, freezeProvider, unfreezeProvider, checkProviderHealth, autoConfigureProvider, getConfigurationTaskDetail, testSSHConnection as testSSHConnectionAPI } from '@/api/admin'
import { countries, getFlagEmoji, getCountryByName, getCountriesByRegion } from '@/utils/countries'
import { formatMemorySize, formatDiskSize } from '@/utils/unit-formatter'
import { useUserStore } from '@/pinia/modules/user'

const providers = ref([])
const loading = ref(false)
const showAddDialog = ref(false)
const addProviderLoading = ref(false)
const addProviderFormRef = ref()
const isEditing = ref(false)
const activeConfigTab = ref('basic') // 添加标签页状态

// 分页
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 添加服务器表单
const addProviderForm = reactive({
  id: null,
  name: '',
  type: '',
  host: '',
  port: 22,
  username: '',
  password: '',
  sshKey: '',
  description: '',
  region: '',
  country: '',
  countryCode: '',
  containerEnabled: true,
  vmEnabled: false,
  architecture: 'amd64', // 新增架构字段，默认amd64
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
  ipv4PortMappingMethod: 'device_proxy', // IPv4端口映射方式：device_proxy, iptables, native
  ipv6PortMappingMethod: 'device_proxy',  // IPv6端口映射方式：device_proxy, iptables, native
  executionRule: 'auto', // 操作轮转规则：auto(自动切换), api_only(仅API), ssh_only(仅SSH)
  sshConnectTimeout: 30, // SSH连接超时（秒），默认30秒
  sshExecuteTimeout: 300 // SSH执行超时（秒），默认300秒
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

// 表单验证规则
const addProviderRules = {
  name: [
    { required: true, message: '请输入服务器名称', trigger: 'blur' }
  ],
  type: [
    { required: true, message: '请选择服务器类型', trigger: 'change' }
  ],
  host: [
    { required: true, message: '请输入主机地址', trigger: 'blur' }
  ],
  port: [
    { required: true, message: '请输入端口', trigger: 'blur' }
  ],
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' }
  ],
  architecture: [
    { required: true, message: '请选择服务器架构', trigger: 'change' }
  ],
  status: [
    { required: true, message: '请选择状态', trigger: 'change' }
  ]
}

// 验证至少选择一种虚拟化类型
const validateVirtualizationType = () => {
  if (!addProviderForm.containerEnabled && !addProviderForm.vmEnabled) {
    ElMessage.warning('请至少选择一种虚拟化类型（容器或虚拟机）')
    return false
  }
  return true
}

// 国家列表数据
const countriesData = ref(countries)
const groupedCountries = ref(getCountriesByRegion())

// 获取国旗 emoji (使用工具函数)
// const getFlagEmoji 已从 utils/countries 导入

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
    const response = await getProviderList({
      page: currentPage.value,
      pageSize: pageSize.value
    })
    providers.value = response.data.list || []
    total.value = response.data.total || 0
  } catch (error) {
    ElMessage.error('加载提供商列表失败')
  } finally {
    loading.value = false
  }
}

// SSH连接测试相关
const testingConnection = ref(false)
const connectionTestResult = ref(null)

// 测试SSH连接
const testSSHConnection = async () => {
  if (!addProviderForm.host || !addProviderForm.username || !addProviderForm.password) {
    ElMessage.warning('请先填写主机地址、用户名和密码')
    return
  }

  testingConnection.value = true
  connectionTestResult.value = null

  try {
    const result = await testSSHConnectionAPI({
      host: addProviderForm.host,
      port: addProviderForm.port || 22,
      username: addProviderForm.username,
      password: addProviderForm.password,
      testCount: 3
    })

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
    ElMessage.error('测试失败: ' + error.message)
  } finally {
    testingConnection.value = false
  }
}

// 应用推荐的超时值
const applyRecommendedTimeout = () => {
  if (connectionTestResult.value && connectionTestResult.value.success) {
    addProviderForm.sshConnectTimeout = connectionTestResult.value.recommendedTimeout
    addProviderForm.sshExecuteTimeout = Math.max(300, connectionTestResult.value.recommendedTimeout * 10)
    ElMessage.success('已应用推荐的超时配置')
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
    port: 22,
    username: '',
    password: '',
    sshKey: '',
    description: '',
    region: '',
    country: '',
    countryCode: '',
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
    ipv4PortMappingMethod: 'device_proxy',
    ipv6PortMappingMethod: 'device_proxy',
    // 重置SSH超时配置
    sshConnectTimeout: 30,
    sshExecuteTimeout: 300
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
    
    addProviderLoading.value = true

    const serverData = {
      name: addProviderForm.name,
      type: addProviderForm.type,
      endpoint: addProviderForm.host, // 只存储主机地址
      sshPort: addProviderForm.port, // 单独存储SSH端口
      username: addProviderForm.username,
      config: addProviderForm.sshKey ? JSON.stringify({ ssh_key: addProviderForm.sshKey }) : '',
      region: addProviderForm.region,
      country: addProviderForm.country,
      countryCode: addProviderForm.countryCode,
      container_enabled: addProviderForm.containerEnabled,
      vm_enabled: addProviderForm.vmEnabled,
      architecture: addProviderForm.architecture, // 添加架构字段
      totalQuota: 0,
      allowClaim: true,
      expiresAt: addProviderForm.expiresAt || '', // 添加过期时间字段
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
      // 操作执行规则
      executionRule: addProviderForm.executionRule || 'auto', // 操作轮转规则
      // SSH超时配置
      sshConnectTimeout: addProviderForm.sshConnectTimeout || 30,
      sshExecuteTimeout: addProviderForm.sshExecuteTimeout || 300
    }

    // 根据Provider类型设置端口映射方式
    if (addProviderForm.type === 'docker') {
      // Docker使用原生实现，不可选择
      serverData.ipv4PortMappingMethod = 'native'
      serverData.ipv6PortMappingMethod = 'native'
    } else if (addProviderForm.type === 'proxmox') {
      // Proxmox IPv4: NAT情况下默认iptables，独立IP情况下可选
      if (addProviderForm.networkType === 'nat_ipv4' || addProviderForm.networkType === 'nat_ipv4_ipv6') {
        serverData.ipv4PortMappingMethod = 'iptables'
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

    // 只有在非编辑模式或者输入了密码时才包含密码
    if (!isEditing.value || addProviderForm.password) {
      serverData.password = addProviderForm.password
    }

    if (isEditing.value) {
      // 编辑服务器时需要添加 status 字段
      serverData.status = addProviderForm.status
      await updateProvider(addProviderForm.id, serverData)
      ElMessage.success('服务器更新成功')
    } else {
      // 添加服务器
      await createProvider(serverData)
      ElMessage.success('服务器添加成功')
    }
    
    cancelAddServer()
    await loadProviders()
  } catch (error) {
    console.error('Provider操作失败:', error)
    const errorMsg = error.response?.data?.msg || error.message || (isEditing.value ? '服务器更新失败' : '服务器添加失败')
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
  
  // 解析config获取SSH key
  let sshKey = ''
  try {
    const config = JSON.parse(provider.config || '{}')
    sshKey = config.ssh_key || ''
  } catch (e) {
    // ignore parsing error
  }
  
  // 填充表单数据
  Object.assign(addProviderForm, {
    id: provider.id,
    name: provider.name,
    type: provider.type,
    host: host,
    port: parseInt(port) || 22,
    username: provider.username || '',
    password: '', // 编辑时不显示密码
    sshKey: sshKey,
    description: provider.description || '',
    region: provider.region || '',
    country: provider.country || '',
    countryCode: provider.countryCode || '',
    containerEnabled: provider.container_enabled !== false,
    vmEnabled: provider.vm_enabled === true,
    architecture: provider.architecture || 'amd64', // 添加架构字段
    status: provider.status || 'active',
    expiresAt: provider.expiresAt || '', // 添加过期时间字段
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
    // 操作执行规则
    executionRule: provider.executionRule || 'auto', // 默认自动切换
    // SSH超时配置
    sshConnectTimeout: provider.sshConnectTimeout || 30,
    sshExecuteTimeout: provider.sshExecuteTimeout || 300
  })

  // 根据Provider类型设置端口映射方式的默认值
  if (provider.type === 'docker') {
    addProviderForm.ipv4PortMappingMethod = 'native' // Docker使用原生实现
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
    ElMessage.success('服务器删除成功')
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('服务器删除失败')
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
    ElMessage.success('服务器已冻结')
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('服务器冻结失败')
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
        inputErrorMessage: '请输入正确的日期格式或留空',
        inputPlaceholder: '如：2024-12-31 23:59:59 或留空'
      }
    )

    await unfreezeProvider(server.id, expiresAt || '')
    ElMessage.success('服务器已解冻')
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('服务器解冻失败')
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
        ElMessage.info('有正在运行的任务，将显示任务日志')
        await viewTaskLog(result.runningTask.id)
        return
      }
      
      return
    }

    // 如果没有历史记录，直接开始配置
    await startNewConfiguration(provider, force)

  } catch (error) {
    console.error('检查配置状态失败:', error)
    ElMessage.error('检查配置状态失败: ' + (error.message || '未知错误'))
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
      message: '正在执行自动配置，请稍候...',
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
      ElMessage.error('启动配置失败: ' + (error.message || '未知错误'))
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
    message: '正在进行健康检查，请稍候...',
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
      ElMessage.success('健康检查完成')
      await loadProviders() // 重新加载提供商列表以更新状态
    } else {
      console.log('健康检查失败，code:', result.code, 'msg:', result.msg)
      ElMessage.error(result.msg || '健康检查失败')
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
    console.log('任务详情API响应:', response) // 添加调试日志
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
const copyTaskLog = () => {
  if (taskLogDialog.task && taskLogDialog.task.logOutput) {
    navigator.clipboard.writeText(taskLogDialog.task.logOutput).then(() => {
      ElMessage.success('日志已复制到剪贴板')
    }).catch(() => {
      ElMessage.error('复制失败')
    })
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
.providers-container {
  padding: 20px;
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
</style>
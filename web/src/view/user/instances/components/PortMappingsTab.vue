<template>
  <div class="ports-content">
    <div class="ports-header">
      <div class="ports-summary">
        <div class="summary-item">
          <span class="label">{{ $t('user.instanceDetail.publicIP') }}:</span>
          <span class="value">{{ instance.publicIP || $t('user.instanceDetail.none') }}</span>
        </div>
        <div class="summary-item">
          <span class="label">{{ $t('user.instances.portMapping') }}:</span>
          <span class="value">{{ portMappings.length }}{{ $t('common.items') }}</span>
        </div>
      </div>
      <el-button
        type="primary"
        size="small"
        @click="$emit('refresh')"
      >
        <el-icon><Refresh /></el-icon>
        {{ $t('user.instances.search') }}
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
        :label="$t('user.instanceDetail.portType')"
        width="110"
      >
        <template #default="{ row }">
          <el-tag
            size="small"
            :type="row.portType === 'manual' ? 'warning' : 'success'"
          >
            {{ row.portType === 'manual' ? $t('user.instanceDetail.manualAdd') : $t('user.instanceDetail.rangeMapping') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        prop="mappingType"
        :label="$t('user.instanceDetail.mappingSource')"
        width="110"
      >
        <template #default="{ row }">
          <el-tag
            size="small"
            :type="row.mappingType === 'controller' ? 'warning' : 'primary'"
          >
            {{ row.mappingType === 'controller' ? $t('user.instanceDetail.controllerForwarding') : $t('user.instanceDetail.nodeForwarding') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        prop="hostPort"
        :label="$t('user.instanceDetail.publicPort')"
        width="110"
      />
      <el-table-column
        prop="guestPort"
        :label="$t('user.instanceDetail.internalPort')"
        width="110"
      />
      <el-table-column
        prop="protocol"
        :label="$t('user.instanceDetail.protocol')"
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
        :label="$t('user.instanceDetail.status')"
        width="100"
      >
        <template #default="{ row }">
          <el-tag
            size="small"
            :type="row.status === 'active' ? 'success' : 'info'"
          >
            {{ row.status === 'active' ? $t('user.instanceDetail.active') : $t('user.instanceDetail.unused') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('user.instanceDetail.connectionInfo')"
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
                :title="$t('user.instanceDetail.controllerSSHHint')"
              >
                {{ $t('user.instanceDetail.controllerForwardingSSH', { port: row.hostPort }) }}
              </span>
              <el-tag
                size="small"
                type="warning"
                style="margin-left: 8px;"
              >
                {{ $t('user.instanceDetail.controllerForwarding') }}
              </el-tag>
            </div>
            <!-- 控制端转发模式（非SSH端口） -->
            <div
              v-else-if="row.mappingType === 'controller'"
              class="port-access"
            >
              <span
                class="command-text"
                :title="$t('user.instanceDetail.controllerPortHint', { port: row.hostPort })"
              >
                {{ $t('user.instanceDetail.controllerForwardingPort', { port: row.hostPort }) }}
              </span>
              <el-tag
                size="small"
                type="warning"
                style="margin-left: 8px;"
              >
                {{ $t('user.instanceDetail.controllerForwarding') }}
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
                @click="$emit('copy', `ssh ${instance.username || 'root'}@${instance.publicIP} -p ${row.hostPort}`)"
              >
                {{ $t('user.instanceDetail.copy') }}
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
                @click="$emit('copy', `${instance.publicIP}:${row.hostPort}`)"
              >
                {{ $t('user.instanceDetail.copy') }}
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
      <p>{{ $t('user.instances.portMapping') }}</p>
    </div>
  </div>
</template>

<script setup>
import { useI18n } from 'vue-i18n'
const { t } = useI18n()

defineProps({
  instance: { type: Object, required: true },
  portMappings: { type: Array, default: () => [] }
})

defineEmits(['refresh', 'copy'])

const formatSSHCommand = (username, ip, port) => {
  const user = username || 'root'
  const host = ip || ''
  return `ssh ${user}@${host} -p ${port}`
}

const formatIPPort = (ip, port) => {
  return `${ip || ''}:${port}`
}
</script>

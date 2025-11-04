<template>
  <Teleport to="body">
    <!-- 所有最小化的SSH连接 -->
    <div 
      v-for="conn in minimizedConnections" 
      :key="conn.instanceId"
      class="ssh-minimized-container"
      :style="{ bottom: `${20 + minimizedConnections.indexOf(conn) * 60}px` }"
    >
      <div class="ssh-minimized-header" @click="restoreConnection(conn.instanceId)">
        <span>SSH Terminal - {{ conn.instanceName }}</span>
        <el-button 
          :icon="Close"
          text
          size="small" 
          @click.stop="closeConnection(conn.instanceId)"
          class="close-btn"
        />
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { computed } from 'vue'
import { Close } from '@element-plus/icons-vue'
import { useSSHStore } from '@/pinia/modules/ssh'
import { useRouter } from 'vue-router'

const sshStore = useSSHStore()
const router = useRouter()

const minimizedConnections = computed(() => sshStore.minimizedConnections)

const restoreConnection = (instanceId) => {
  // 导航到实例详情页并恢复SSH
  router.push(`/user/instances/${instanceId}`)
  setTimeout(() => {
    sshStore.showConnection(instanceId)
  }, 100)
}

const closeConnection = (instanceId) => {
  sshStore.closeConnection(instanceId)
}
</script>

<style scoped>
/* 最小化SSH终端样式 - 右下角悬浮 */
.ssh-minimized-container {
  position: fixed;
  right: 20px;
  z-index: 9999;
  background-color: #ffffff;
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  cursor: pointer;
  transition: all 0.3s ease;
  border: 1px solid #e0e0e0;
}

.ssh-minimized-container:hover {
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.2);
  transform: translateY(-2px);
}

.ssh-minimized-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  color: #000000;
  font-size: 14px;
  font-weight: 600;
  min-width: 280px;
  background-color: #ffffff;
  border-radius: 8px;
}

.ssh-minimized-header span {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-right: 10px;
}

.ssh-minimized-header .close-btn {
  color: #666666;
  padding: 4px;
}

.ssh-minimized-header .close-btn:hover {
  color: #000000;
  background-color: #f0f0f0;
}
</style>

<template>
  <div class="resources-page">
    <div class="page-header">
      <h1>资源申领</h1>
      <p>根据您的配额等级，选择合适的服务器资源</p>
    </div>

    <!-- 筛选器 -->
    <div class="filters">
      <el-card>
        <div class="filter-row">
          <div class="filter-item">
            <el-select
              v-model="filters.country"
              placeholder="选择国家/地区"
              clearable
              style="width: 200px;"
            >
              <el-option
                v-for="country in countries"
                :key="country.code"
                :label="country.name"
                :value="country.code"
              >
                <span class="country-option">
                  <img
                    :src="country.flag"
                    :alt="country.name"
                    class="flag-icon"
                  >
                  {{ country.name }}
                </span>
              </el-option>
            </el-select>
          </div>
          
          <div class="filter-item">
            <el-select
              v-model="filters.type"
              placeholder="选择类型"
              clearable
              style="width: 150px;"
            >
              <el-option
                label="容器"
                value="container"
              />
              <el-option
                label="虚拟机"
                value="vm"
              />
            </el-select>
          </div>
          
          <div class="filter-item">
            <el-select
              v-model="filters.spec"
              placeholder="选择规格"
              clearable
              style="width: 150px;"
            >
              <el-option
                label="1核1G"
                value="1c1g"
              />
              <el-option
                label="1核2G"
                value="1c2g"
              />
              <el-option
                label="2核2G"
                value="2c2g"
              />
              <el-option
                label="2核4G"
                value="2c4g"
              />
            </el-select>
          </div>
          
          <el-button
            type="primary"
            @click="loadResources"
          >
            <el-icon><Search /></el-icon>
            搜索
          </el-button>
        </div>
      </el-card>
    </div>

    <!-- 配额提示 -->
    <div class="quota-info">
      <el-alert
        :title="`当前配额等级：${userQuota.level}，剩余配额：${userQuota.remaining}/${userQuota.total}`"
        type="info"
        :closable="false"
        show-icon
      />
    </div>

    <!-- 资源列表 -->
    <div class="resources-grid">
      <div
        v-for="resource in resources"
        :key="resource.id"
        class="resource-card"
        :class="{ 'disabled': !resource.available || userQuota.remaining <= 0 }"
      >
        <div class="resource-header">
          <div class="country-info">
            <img
              :src="resource.countryFlag"
              :alt="resource.countryName"
              class="flag-icon"
            >
            <span class="country-name">{{ resource.countryName }}</span>
          </div>
          <el-tag
            :type="resource.available ? 'success' : 'danger'"
            size="small"
          >
            {{ resource.available ? '可用' : '不可用' }}
          </el-tag>
        </div>
        
        <div class="resource-content">
          <h3>{{ resource.name }}</h3>
          <div class="resource-specs">
            <div class="spec-item">
              <el-icon><Cpu /></el-icon>
              <span>{{ resource.cpu }}核</span>
            </div>
            <div class="spec-item">
              <el-icon><Monitor /></el-icon>
              <span>{{ resource.memory }}GB</span>
            </div>
            <div class="spec-item">
              <el-icon><Files /></el-icon>
              <span>{{ resource.storage }}GB</span>
            </div>
            <div class="spec-item">
              <el-icon><Connection /></el-icon>
              <span>{{ resource.bandwidth }}Mbps</span>
            </div>
          </div>
          
          <div class="resource-type">
            <el-tag :type="resource.type === 'container' ? 'primary' : 'warning'">
              {{ resource.type === 'container' ? '容器' : '虚拟机' }}
            </el-tag>
          </div>
          
          <div class="resource-location">
            <el-icon><Location /></el-icon>
            <span>{{ resource.countryName || resource.region || '-' }}</span>
          </div>
          
          <div class="resource-description">
            {{ resource.description }}
          </div>
        </div>
        
        <div class="resource-footer">
          <div class="quota-cost">
            <span>配额消耗：{{ resource.quotaCost }}</span>
          </div>
          <el-button
            type="primary"
            :disabled="!resource.available || userQuota.remaining <= 0 || resource.quotaCost > userQuota.remaining"
            :loading="resource.claiming"
            @click="claimResource(resource)"
          >
            {{ getClaimButtonText(resource) }}
          </el-button>
        </div>
      </div>
    </div>

    <!-- 空状态 -->
    <div
      v-if="!loading && resources.length === 0"
      class="empty-state"
    >
      <el-empty description="暂无可用资源" />
    </div>

    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-state"
    >
      <el-skeleton
        :rows="3"
        animated
      />
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Search, Cpu, Monitor, Files, Connection, Location
} from '@element-plus/icons-vue'
import { getAvailableResources, claimResource as claimResourceAPI } from '@/api/user'
import { useUserStore } from '@/pinia/modules/user'

const userStore = useUserStore()
const loading = ref(false)
const resources = ref([])

const filters = reactive({
  country: '',
  type: '',
  spec: ''
})

const userQuota = reactive({
  level: 1,
  remaining: 5,
  total: 5
})

const countries = ref([
  { code: 'US', name: '美国', flag: '/flags/us.png' },
  { code: 'JP', name: '日本', flag: '/flags/jp.png' },
  { code: 'SG', name: '新加坡', flag: '/flags/sg.png' },
  { code: 'HK', name: '香港', flag: '/flags/hk.png' },
  { code: 'DE', name: '德国', flag: '/flags/de.png' },
  { code: 'UK', name: '英国', flag: '/flags/uk.png' }
])

const loadResources = async () => {
  loading.value = true
  try {
    const response = await getAvailableResources(filters)
    if (response.code === 0 || response.code === 200) {
      resources.value = response.data.resources || []
      Object.assign(userQuota, response.data.quota || {})
    }
  } catch (error) {
    ElMessage.error('加载资源失败')
  } finally {
    loading.value = false
  }
}

const getClaimButtonText = (resource) => {
  if (!resource.available) return '不可用'
  if (userQuota.remaining <= 0) return '配额不足'
  if (resource.quotaCost > userQuota.remaining) return '配额不足'
  return '申领'
}

const claimResource = async (resource) => {
  try {
    await ElMessageBox.confirm(
      `确定要申领 "${resource.name}" 吗？这将消耗 ${resource.quotaCost} 个配额。`,
      '确认申领',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    resource.claiming = true
    
    const response = await claimResourceAPI({
      resourceId: resource.id,
      type: resource.type
    })
    
    if (response.code === 0 || response.code === 200) {
      ElMessage.success('申领成功！正在为您创建实例...')
      loadResources() // 重新加载资源列表
    } else {
      ElMessage.error(response.msg || '申领失败')
    }
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('申领失败')
    }
  } finally {
    resource.claiming = false
  }
}

onMounted(() => {
  loadResources()
})
</script>

<style scoped>
.resources-page {
  padding: 20px;
}

.page-header {
  margin-bottom: 30px;
}

.page-header h1 {
  margin: 0 0 10px 0;
  color: #303133;
  font-size: 28px;
  font-weight: 600;
}

.page-header p {
  margin: 0;
  color: #606266;
  font-size: 16px;
}

.filters {
  margin-bottom: 20px;
}

.filter-row {
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
}

.filter-item {
  display: flex;
  align-items: center;
  gap: 10px;
}

.filter-item label {
  font-size: 14px;
  color: #606266;
  white-space: nowrap;
}

.country-option {
  display: flex;
  align-items: center;
  gap: 8px;
}

.flag-icon {
  width: 20px;
  height: 15px;
  object-fit: cover;
  border-radius: 2px;
}

.quota-info {
  margin-bottom: 20px;
}

.resources-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(350px, 1fr));
  gap: 20px;
}

.resource-card {
  background: white;
  border-radius: 8px;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.1);
  overflow: hidden;
  transition: all 0.3s ease;
}

.resource-card:hover:not(.disabled) {
  transform: translateY(-2px);
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.15);
}

.resource-card.disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.resource-header {
  padding: 15px 20px;
  background: #f8f9fa;
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid #ebeef5;
}

.country-info {
  display: flex;
  align-items: center;
  gap: 8px;
}

.country-name {
  font-weight: 600;
  color: #303133;
}

.resource-content {
  padding: 20px;
}

.resource-content h3 {
  margin: 0 0 15px 0;
  color: #303133;
  font-size: 18px;
  font-weight: 600;
}

.resource-specs {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 10px;
  margin-bottom: 15px;
}

.spec-item {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 14px;
  color: #606266;
}

.resource-type {
  margin-bottom: 10px;
}

.resource-location {
  display: flex;
  align-items: center;
  gap: 5px;
  margin-bottom: 15px;
  font-size: 14px;
  color: #909399;
}

.resource-description {
  font-size: 14px;
  color: #606266;
  line-height: 1.5;
}

.resource-footer {
  padding: 15px 20px;
  background: #f8f9fa;
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-top: 1px solid #ebeef5;
}

.quota-cost {
  font-size: 14px;
  color: #e6a23c;
  font-weight: 600;
}

.empty-state,
.loading-state {
  margin-top: 50px;
}
</style>
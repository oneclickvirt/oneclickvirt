<template>
  <div class="admin-dashboard">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.dashboard.title') }}</span>
        </div>
      </template>

      <!-- 统计卡片 -->
      <el-row
        :gutter="20"
        class="stats-row"
      >
        <el-col
          :xs="24"
          :sm="12"
          :md="12"
          :lg="6"
          :xl="6"
        >
          <el-card class="stat-card">
            <div class="stat-content">
              <div class="stat-icon user-icon">
                <i class="fas fa-users" />
              </div>
              <div class="stat-info">
                <div class="stat-number">
                  {{ dashboardData.totalUsers }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.dashboard.totalUsers') }}
                </div>
              </div>
            </div>
          </el-card>
        </el-col>
      
        <el-col
          :xs="24"
          :sm="12"
          :md="12"
          :lg="6"
          :xl="6"
        >
          <el-card class="stat-card">
            <div class="stat-content">
              <div class="stat-icon server-icon">
                <i class="fas fa-server" />
              </div>
              <div class="stat-info">
                <div class="stat-number">
                  {{ dashboardData.totalProviders }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.dashboard.totalProviders') }}
                </div>
              </div>
            </div>
          </el-card>
        </el-col>
      
        <el-col
          :xs="24"
          :sm="12"
          :md="12"
          :lg="6"
          :xl="6"
        >
          <el-card class="stat-card">
            <div class="stat-content">
              <div class="stat-icon vm-icon">
                <i class="fas fa-desktop" />
              </div>
              <div class="stat-info">
                <div class="stat-number">
                  {{ dashboardData.totalVMs }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.dashboard.totalVMs') }}
                </div>
              </div>
            </div>
          </el-card>
        </el-col>
      
        <el-col
          :xs="24"
          :sm="12"
          :md="12"
          :lg="6"
          :xl="6"
        >
          <el-card class="stat-card">
            <div class="stat-content">
              <div class="stat-icon container-icon">
                <i class="fas fa-box" />
              </div>
              <div class="stat-info">
                <div class="stat-number">
                  {{ dashboardData.totalContainers }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.dashboard.totalContainers') }}
                </div>
              </div>
            </div>
          </el-card>
        </el-col>
      </el-row>
    </el-card>
  </div>
</template>

<script setup>
import { reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getAdminDashboard } from '@/api/admin'

const { t } = useI18n()

const dashboardData = reactive({
  totalUsers: 0,
  totalProviders: 0,
  totalVMs: 0,
  totalContainers: 0
})

const fetchDashboardData = async () => {
  try {
    const response = await getAdminDashboard()
    if (response.code === 0 || response.code === 200) {
      // 数据在 response.data.statistics 中
      if (response.data && response.data.statistics) {
        Object.assign(dashboardData, response.data.statistics)
      } else {
        // 兼容旧格式，数据直接在 response.data 中
        Object.assign(dashboardData, response.data)
      }
    }
  } catch (error) {
    ElMessage.error(t('admin.dashboard.loadDataFailed'))
    console.error('Dashboard data fetch error:', error)
  }
}

onMounted(async () => {
  await fetchDashboardData()
})
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

.stats-row {
  margin-bottom: 30px;
}

.stat-card {
  height: 140px;
  border-radius: 12px;
  transition: all 0.3s ease;
  cursor: pointer;
}

.stat-card:hover {
  transform: translateY(-5px);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
}

.stat-content {
  display: flex;
  align-items: center;
  height: 100%;
  padding: 10px;
}

.stat-icon {
  width: 70px;
  height: 70px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-right: 20px;
  color: white;
  flex-shrink: 0;
  font-size: 32px;
}

.user-icon {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  box-shadow: 0 4px 12px rgba(102, 126, 234, 0.4);
}

.server-icon {
  background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%);
  box-shadow: 0 4px 12px rgba(240, 147, 251, 0.4);
}

.vm-icon {
  background: linear-gradient(135deg, #4facfe 0%, #00f2fe 100%);
  box-shadow: 0 4px 12px rgba(79, 172, 254, 0.4);
}

.container-icon {
  background: linear-gradient(135deg, #43e97b 0%, #38f9d7 100%);
  box-shadow: 0 4px 12px rgba(67, 233, 123, 0.4);
}

.stat-info {
  flex: 1;
  min-width: 0;
}

.stat-number {
  font-size: 36px;
  font-weight: 700;
  color: #303133;
  line-height: 1.2;
  margin-bottom: 8px;
}

.stat-label {
  font-size: 14px;
  color: #909399;
  font-weight: 500;
}

/* 响应式适配 */
/* 平板端适配 */
@media (max-width: 1024px) {
  .stat-card {
    height: 120px;
    margin-bottom: 16px;
  }
  
  .stat-icon {
    width: 60px;
    height: 60px;
    font-size: 28px;
    margin-right: 16px;
  }
  
  .stat-number {
    font-size: 28px;
  }
  
  .stat-label {
    font-size: 13px;
  }
}

/* 移动端适配 */
@media (max-width: 768px) {
  .stats-row {
    margin-bottom: 20px;
  }
  
  .stat-card {
    height: auto;
    min-height: 100px;
    margin-bottom: 12px;
  }
  
  .stat-card:hover {
    transform: none;
  }
  
  .stat-content {
    padding: 16px;
  }
  
  .stat-icon {
    width: 50px;
    height: 50px;
    font-size: 24px;
    margin-right: 12px;
  }
  
  .stat-number {
    font-size: 24px;
  }
  
  .stat-label {
    font-size: 12px;
  }
}

/* 小屏移动端适配 */
@media (max-width: 480px) {
  .stat-content {
    padding: 12px;
  }
  
  .stat-icon {
    width: 45px;
    height: 45px;
    font-size: 20px;
    margin-right: 10px;
  }
  
  .stat-number {
    font-size: 20px;
  }
  
  .stat-label {
    font-size: 11px;
  }
}
</style>
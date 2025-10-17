<template>
  <div class="navbar">
    <div class="right-menu">
      <el-dropdown
        class="avatar-container"
        trigger="click"
      >
        <div class="avatar-wrapper">
          <el-avatar
            :size="40"
            :src="userInfo.headerImg || ''"
          >
            <el-icon><User /></el-icon>
          </el-avatar>
          <span class="username">{{ userInfo.nickname || userInfo.username }}</span>
          <el-icon class="el-icon-caret-bottom">
            <CaretBottom />
          </el-icon>
        </div>
        <template #dropdown>
          <el-dropdown-menu>
            <!-- 管理员视图切换按钮 -->
            <el-dropdown-item
              v-if="userStore.canSwitchViewMode"
              @click="toggleViewMode"
            >
              <el-icon style="margin-right: 8px;">
                <Switch />
              </el-icon>
              <span>切换到{{ userStore.currentViewMode === 'admin' ? '用户' : '管理员' }}视图</span>
            </el-dropdown-item>
            <el-dropdown-item
              @click="logout"
              divided
            >
              <el-icon style="margin-right: 8px;">
                <SwitchButton />
              </el-icon>
              <span>退出登录</span>
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessageBox, ElMessage } from 'element-plus'
import { Switch, SwitchButton, User, CaretBottom } from '@element-plus/icons-vue'
import { useUserStore } from '@/pinia/modules/user'

const router = useRouter()
const userStore = useUserStore()

const userInfo = computed(() => userStore.user || {})

const toggleViewMode = () => {
  if (!userStore.canSwitchViewMode) {
    ElMessage.warning('只有管理员可以切换视图模式')
    return
  }
  
  const newMode = userStore.currentViewMode === 'admin' ? 'user' : 'admin'
  const success = userStore.switchViewMode(newMode)
  
  if (success) {
    ElMessage.success(`已切换到${newMode === 'admin' ? '管理员' : '用户'}视图`)
    
    // 跳转到对应的首页
    const targetPath = newMode === 'admin' ? '/admin/dashboard' : '/user/dashboard'
    router.push(targetPath)
  }
}

const logout = async () => {
  try {
    await ElMessageBox.confirm('确定注销并退出系统吗？', '提示', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    })
    
    userStore.logout()
    router.push('/home')
  } catch (error) {
  }
}
</script>

<style lang="scss" scoped>
.navbar {
  height: 50px;
  overflow: hidden;
  position: relative;
  background: #fff;
  box-shadow: 0 1px 4px rgba(0,21,41,.08);
  display: flex;
  align-items: center;
  justify-content: flex-end;
  padding-right: 20px;

  .right-menu {
    float: right;
    height: 100%;
    line-height: 50px;
    display: flex;
    align-items: center;

    &:focus {
      outline: none;
    }

    .right-menu-item {
      display: inline-block;
      padding: 0 8px;
      height: 100%;
      font-size: 18px;
      color: #5a5e66;
      vertical-align: text-bottom;

      &.hover-effect {
        cursor: pointer;
        transition: background .3s;

        &:hover {
          background: rgba(0, 0, 0, .025)
        }
      }
    }

    .avatar-container {
      margin-right: 30px;

      .avatar-wrapper {
        margin-top: 5px;
        position: relative;
        display: flex;
        align-items: center;
        cursor: pointer;

        .username {
          margin-left: 10px;
          margin-right: 5px;
        }

        .el-icon-caret-bottom {
          cursor: pointer;
          position: absolute;
          right: -20px;
          top: 25px;
          font-size: 12px;
        }
      }
    }
    
    /* 移除logout-button相关样式 */
  }
}
</style>
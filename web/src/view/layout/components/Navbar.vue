<template>
  <div class="navbar">
    <!-- 移动端汉堡菜单按钮 -->
    <div class="hamburger-container">
      <el-button
        class="hamburger-btn"
        :icon="Menu"
        circle
        @click="toggleSidebar"
      />
    </div>
    
    <div class="right-menu">
      <!-- 语言切换按钮 -->
      <div class="language-switcher">
        <el-button
          :title="t('navbar.switchLanguage')"
          @click="switchLanguage"
        >
          <el-icon><Operation /></el-icon>
          <span class="language-text">{{ languageStore.currentLanguage === 'zh-CN' ? 'English' : '中文' }}</span>
        </el-button>
      </div>

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
              <span>{{ t('navbar.switchTo') }}{{ userStore.currentViewMode === 'admin' ? t('navbar.userView') : t('navbar.adminView') }}</span>
            </el-dropdown-item>
            <el-dropdown-item
              divided
              @click="logout"
            >
              <el-icon style="margin-right: 8px;">
                <SwitchButton />
              </el-icon>
              <span>{{ t('common.logout') }}</span>
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
import { useI18n } from 'vue-i18n'
import { ElMessageBox, ElMessage } from 'element-plus'
import { Switch, SwitchButton, User, CaretBottom, Menu, Operation } from '@element-plus/icons-vue'
import { useUserStore } from '@/pinia/modules/user'
import { useLanguageStore } from '@/pinia/modules/language'

const emit = defineEmits(['toggle-sidebar'])
const router = useRouter()
const userStore = useUserStore()
const languageStore = useLanguageStore()
const { t, locale } = useI18n()

const userInfo = computed(() => userStore.user || {})

const toggleSidebar = () => {
  emit('toggle-sidebar')
}

const switchLanguage = () => {
  const newLang = languageStore.toggleLanguage()
  locale.value = newLang
  ElMessage.success(t('navbar.languageSwitched'))
}

const toggleViewMode = () => {
  if (!userStore.canSwitchViewMode) {
    ElMessage.warning(t('navbar.onlyAdminCanSwitch'))
    return
  }
  
  const newMode = userStore.currentViewMode === 'admin' ? 'user' : 'admin'
  const success = userStore.switchViewMode(newMode)
  
  if (success) {
    const viewName = newMode === 'admin' ? t('navbar.adminView') : t('navbar.userView')
    ElMessage.success(`${t('navbar.switchedTo')}${viewName}`)
    
    const targetPath = newMode === 'admin' ? '/admin/dashboard' : '/user/dashboard'
    router.push(targetPath)
  }
}

const logout = async () => {
  try {
    await ElMessageBox.confirm(t('navbar.confirmLogout'), t('navbar.tip'), {
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
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
  height: var(--navbar-height);
  overflow: hidden;
  position: relative;
  background: #fff;
  box-shadow: 0 1px 4px rgba(0,21,41,.08);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;

  .hamburger-container {
    display: none;
    
    .hamburger-btn {
      color: var(--text-color-primary);
      background: transparent;
      border: none;
      
      &:hover {
        background: var(--bg-color-hover);
      }
    }
  }

  .right-menu {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-left: auto;

    &:focus {
      outline: none;
    }

    .language-switcher {
      display: flex;
      align-items: center;

      .el-button {
        color: var(--text-color-primary);
        background: transparent;
        border: 1px solid var(--border-color-base);
        display: flex;
        align-items: center;
        gap: 6px;
        padding: 8px 15px;
        
        &:hover {
          background: var(--bg-color-hover);
          border-color: var(--el-color-primary);
        }

        .language-text {
          font-size: 14px;
          font-weight: 500;
        }
      }
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
      .avatar-wrapper {
        position: relative;
        display: flex;
        align-items: center;
        cursor: pointer;

        .username {
          margin-left: 10px;
          margin-right: 5px;
          font-size: var(--font-size-sm);
        }

        .el-icon-caret-bottom {
          cursor: pointer;
          font-size: 12px;
          margin-left: 4px;
        }
      }
    }
  }
}

/* 平板和移动端适配 */
@media (max-width: 1024px) {
  .navbar {
    .hamburger-container {
      display: block;
    }
    
    .right-menu {
      .avatar-container .avatar-wrapper .username {
        display: none;
      }
    }
  }
}

/* 移动端适配 */
@media (max-width: 768px) {
  .navbar {
    padding: 0 12px;
    height: var(--navbar-height);
    
    .right-menu {
      gap: 8px;

      .avatar-container {
        .avatar-wrapper {
          .el-avatar {
            width: 32px !important;
            height: 32px !important;
          }
          
          .el-icon-caret-bottom {
            display: none;
          }
        }
      }
    }
  }
}
</style>
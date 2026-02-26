<template>
  <div class="home-container">
    <!-- 导航栏 -->
    <header class="home-header">
      <div class="header-content">
        <div class="logo">
          <img
            src="@/assets/images/logo.png"
            alt="OneClickVirt Logo"
            class="logo-image"
          >
          <h1>{{ t('home.title') }}</h1>
        </div>
        <nav class="nav-menu">
          <!-- 主题切换按钮 -->
          <button
            class="nav-link theme-btn"
            :title="themeStore.isDark ? t('navbar.lightMode') : t('navbar.darkMode')"
            @click="toggleTheme"
          >
            <el-icon><component :is="themeStore.isDark ? Sunny : Moon" /></el-icon>
          </button>
          <!-- 语言切换按钮 -->
          <button
            class="nav-link language-btn"
            @click="switchLanguage"
          >
            <el-icon><Operation /></el-icon>
            {{ languageStore.currentLanguage === 'zh-CN' ? 'English' : '中文' }}
          </button>
          <router-link
            to="/login"
            class="nav-link"
          >
            {{ t('home.nav.login') }}
          </router-link>
          <router-link
            to="/register"
            class="nav-link primary"
          >
            {{ t('home.nav.register') }}
          </router-link>
        </nav>
      </div>
    </header>
    
    <!-- 主要内容 -->
    <main class="home-main">
      <!-- 英雄区域 -->
      <section class="hero-section">
        <div class="hero-content">
          <h1 class="hero-title">
            {{ t('home.hero.title') }}
          </h1>
          <p class="hero-description">
            {{ t('home.hero.description') }}
          </p>
          <div class="hero-actions">
            <router-link
              to="/login"
              class="btn btn-primary"
            >
              {{ t('home.hero.loginButton') }}
            </router-link>
            <router-link
              to="/register"
              class="btn btn-secondary"
            >
              {{ t('home.hero.registerButton') }}
            </router-link>
          </div>
        </div>
        <div class="hero-image">
          <div class="feature-preview">
            <div class="preview-card">
              <div class="card-icon">
                <i class="fas fa-server" />
              </div>
              <h3>{{ t('home.features.vm.title') }}</h3>
              <p>{{ t('home.features.vm.description') }}</p>
            </div>
            <div class="preview-card">
              <div class="card-icon">
                <i class="fas fa-box" />
              </div>
              <h3>{{ t('home.features.container.title') }}</h3>
              <p>{{ t('home.features.container.description') }}</p>
            </div>
            <div class="preview-card">
              <div class="card-icon">
                <i class="fas fa-chart-bar" />
              </div>
              <h3>{{ t('home.features.monitoring.title') }}</h3>
              <p>{{ t('home.features.monitoring.description') }}</p>
            </div>
          </div>
        </div>
      </section>

      <!-- 支持的虚拟化平台 -->
      <section class="platforms-section">
        <div class="section-header">
          <h2>{{ t('home.platforms.title') }}</h2>
          <p>{{ t('home.platforms.description') }}</p>
        </div>
        <div class="platforms-grid">
          <div class="platform-item">
            <div class="platform-icon pve-icon">
              <img
                src="@/assets/images/proxmox.png"
                alt="Proxmox VE"
                width="60"
                height="60"
              >
            </div>
            <h3>Proxmox VE</h3>
          </div>
          
          <div class="platform-item">
            <div class="platform-icon incus-icon">
              <img
                src="@/assets/images/incus.png"
                alt="Incus"
                width="60"
                height="60"
              >
            </div>
            <h3>Incus</h3>
          </div>
          
          <div class="platform-item">
            <div class="platform-icon docker-icon">
              <img
                src="@/assets/images/docker.png"
                alt="Docker"
                width="60"
                height="60"
              >
            </div>
            <h3>Docker</h3>
          </div>
          
          <div class="platform-item">
            <div class="platform-icon lxd-icon">
              <img
                src="@/assets/images/lxd.png"
                alt="LXD"
                width="60"
                height="60"
              >
            </div>
            <h3>LXD</h3>
          </div>
        </div>
        <!-- 统计概况：与平台卡片相同的框架风格，显示用户/节点/容器/虚拟机数量 -->
        <div
          class="stats-grid"
          aria-label="platform-stats"
        >
          <div class="platform-item stats-item">
            <div class="platform-icon">
              <i
                class="fas fa-users fa-2x"
                aria-hidden="true"
              />
            </div>
            <h3>{{ t('home.stats.users') }}</h3>
            <p class="stats-value">
              {{ usersCountDisplay }}
            </p>
          </div>

          <div class="platform-item stats-item">
            <div class="platform-icon">
              <i
                class="fas fa-network-wired fa-2x"
                aria-hidden="true"
              />
            </div>
            <h3>{{ t('home.stats.nodes') }}</h3>
            <p class="stats-value">
              {{ nodesCountDisplay }}
            </p>
          </div>

          <div class="platform-item stats-item">
            <div class="platform-icon">
              <i
                class="fas fa-box fa-2x"
                aria-hidden="true"
              />
            </div>
            <h3>{{ t('home.stats.containers') }}</h3>
            <p class="stats-value">
              {{ containersCountDisplay }}
            </p>
          </div>

          <div class="platform-item stats-item">
            <div class="platform-icon">
              <i
                class="fas fa-server fa-2x"
                aria-hidden="true"
              />
            </div>
            <h3>{{ t('home.stats.vms') }}</h3>
            <p class="stats-value">
              {{ vmsCountDisplay }}
            </p>
          </div>
        </div>
      </section>

      <!-- 系统公告 -->
      <section
        v-if="announcements.length > 0"
        class="announcements-section"
      >
        <div class="section-header">
          <h2>{{ t('home.announcements.title') }}</h2>
        </div>
        <div class="announcements-list">
          <div
            v-for="announcement in announcements"
            :key="announcement.id"
            class="announcement-item"
          >
            <div class="announcement-header">
              <h3>{{ announcement.title }}</h3>
              <div class="announcement-meta">
                <el-tag
                  :type="announcement.type === 'homepage' ? 'success' : 'warning'"
                  size="small"
                >
                  {{ announcement.type === 'homepage' ? t('home.announcements.typeHomepage') : t('home.announcements.typeTopbar') }}
                </el-tag>
                <span class="announcement-date">{{ formatDate(announcement.createdAt) }}</span>
              </div>
            </div>
            <div
              class="announcement-content"
              v-html="announcement.contentHtml || announcement.content"
            />
          </div>
        </div>
      </section>
    </main>
    
    <!-- 页脚 -->
    <footer class="home-footer">
      <div class="footer-glow-top" />
      <div class="footer-inner">
        <div class="footer-brand">
          <div class="footer-logo">
            <img
              src="@/assets/images/logo.png"
              alt="OneClickVirt Logo"
              class="footer-logo-img"
            >
            <span class="footer-logo-text">OneClickVirt</span>
          </div>
          <p class="footer-tagline">
            {{ t('home.hero.description') }}
          </p>
          <a
            href="https://github.com/oneclickvirt"
            target="_blank"
            rel="noopener noreferrer"
            class="footer-github-btn"
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="currentColor"
            >
              <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
            </svg>
            GitHub
          </a>
        </div>

        <div class="footer-links-grid">
          <div class="footer-col">
            <h4 class="footer-col-title">
              <span class="footer-col-dot" />
              {{ t('home.footer.coreProjects') }}
            </h4>
            <ul class="footer-link-list">
              <li>
                <a
                  href="https://github.com/oneclickvirt/oneclickvirt"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>OneClickVirt
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/oneclickvirt/ecs"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>ECS
                </a>
              </li>
            </ul>
          </div>

          <div class="footer-col">
            <h4 class="footer-col-title">
              <span class="footer-col-dot" />
              {{ t('home.footer.relatedProjects') }}
            </h4>
            <ul class="footer-link-list">
              <li>
                <a
                  href="https://github.com/oneclickvirt/pve"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>Proxmox VE
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/oneclickvirt/incus"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>Incus
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/oneclickvirt/docker"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>Docker
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/oneclickvirt/lxd"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>LXD
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/oneclickvirt"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="more-link"
                >
                  <span class="link-arrow">›</span>{{ t('home.footer.moreProjects') }}
                </a>
              </li>
            </ul>
          </div>

          <div class="footer-col">
            <h4 class="footer-col-title">
              <span class="footer-col-dot" />
              {{ t('home.footer.supportAndDocs') }}
            </h4>
            <ul class="footer-link-list">
              <li>
                <a
                  href="https://www.spiritlhl.net/"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>{{ t('home.footer.documentation') }}
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/oneclickvirt/oneclickvirt/issues"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>{{ t('home.footer.feedback') }}
                </a>
              </li>
              <li>
                <a
                  href="https://t.me/oneclickvirt"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <span class="link-arrow">›</span>{{ t('home.footer.communityGroup') }}
                </a>
              </li>
            </ul>
          </div>
        </div>
      </div>

      <div class="footer-bottom">
        <div class="footer-bottom-inner">
          <span class="footer-copyright">&copy; 2026 OneClickVirt. {{ t('home.footer.allRightsReserved') }}</span>
          <span class="footer-divider" />
          <a
            href="https://github.com/oneclickvirt"
            target="_blank"
            rel="noopener noreferrer"
            class="footer-bottom-link"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="currentColor"
              style="margin-right:4px;vertical-align:middle"
            >
              <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
            </svg>
            {{ t('home.footer.openSourceProject') }}
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getPublicAnnouncements, getPublicStats } from '@/api/public'
import { checkSystemInit } from '@/api/init'
import { ElTag, ElMessage } from 'element-plus'
import { Operation, Sunny, Moon } from '@element-plus/icons-vue'
import { useLanguageStore } from '@/pinia/modules/language'
import { useThemeStore } from '@/pinia/modules/theme'

const router = useRouter()
const { t, locale } = useI18n()
const languageStore = useLanguageStore()
const themeStore = useThemeStore()
const announcements = ref([])
// 统计数据
const usersCount = ref(null)
const nodesCount = ref(null)
const containersCount = ref(null)
const vmsCount = ref(null)

const usersCountDisplay = computed(() => (usersCount.value === null ? '-' : usersCount.value))
const nodesCountDisplay = computed(() => (nodesCount.value === null ? '-' : nodesCount.value))
const containersCountDisplay = computed(() => (containersCount.value === null ? '-' : containersCount.value))
const vmsCountDisplay = computed(() => (vmsCount.value === null ? '-' : vmsCount.value))

const switchLanguage = () => {
  const newLang = languageStore.toggleLanguage()
  locale.value = newLang
  ElMessage.success(t('navbar.languageSwitched'))
}

const toggleTheme = () => {
  themeStore.toggleTheme()
}

const formatDate = (dateString) => {
  return new Date(dateString).toLocaleDateString(locale.value === 'zh-CN' ? 'zh-CN' : 'en-US')
}

const fetchAnnouncements = async () => {
  try {
    // 获取首页公告
    const response = await getPublicAnnouncements('homepage')
    if (response.code === 0 || response.code === 200) {
      announcements.value = response.data.slice(0, 3) // 只显示最新3条
    }
  } catch (error) {
    console.error(t('home.errors.fetchAnnouncementsFailed'), error)
  }
}

const fetchPublicStats = async () => {
  try {
    const resp = await getPublicStats()
    if (resp && (resp.code === 0 || resp.code === 200) && resp.data) {
      const d = resp.data
      // 尝试从常见字段拾取数据，做多层回退以兼容不同返回结构
      usersCount.value = d.userStats?.totalUsers ?? d.user_count ?? d.userCount ?? d.userTotal ?? null
      // nodes 可能对应 regionStats 的 count 总和或 provider 总数
      if (Array.isArray(d.regionStats) && d.regionStats.length > 0) {
        let total = 0
        d.regionStats.forEach(r => { total += r.count ?? 0 })
        nodesCount.value = total
      } else {
        nodesCount.value = d.provider_count ?? d.node_count ?? d.nodeCount ?? null
      }

      // 容器/虚拟机：尝试从资源统计中读取
      containersCount.value = d.resourceUsage?.container_count ?? d.resourceUsage?.containerCount ?? d.container_count ?? d.containerCount ?? null
      vmsCount.value = d.resourceUsage?.vm_count ?? d.resourceUsage?.vmCount ?? d.vm_count ?? d.vmCount ?? null
    }
  } catch (error) {
    console.error('获取公开统计数据失败', error)
  }
}

const checkInitStatus = async () => {
  try {
    const response = await checkSystemInit()
    console.log(t('home.debug.checkingInit'), response)
    if (response && response.code === 0 && response.data && response.data.needInit === true) {
      console.log(t('home.debug.needInitRedirect'))
      router.push('/init')
    }
  } catch (error) {
    console.error(t('home.errors.checkInitFailed'), error)
    // 如果是网络错误或服务器错误，可能是数据库未初始化导致的
    if (error.message.includes('Network Error') || 
        error.response?.status >= 500 || 
        error.code === 'ECONNREFUSED') {
      console.warn(t('home.debug.serverConnectionFailed'))
      router.push('/init')
    }
  }
}

onMounted(() => {
  console.log('VITE_BASE_API:', import.meta.env.VITE_BASE_API)
  console.log('VITE_BASE_PATH:', import.meta.env.VITE_BASE_PATH)
  console.log('VITE_SERVER_PORT:', import.meta.env.VITE_SERVER_PORT)
  console.log('All env vars:', import.meta.env)
  
  // 首先检查初始化状态
  checkInitStatus()
  // 然后获取公告
  fetchAnnouncements()
  // 获取公开统计数据（用于未登录首页展示）
  fetchPublicStats()
})
</script>

<style scoped>
.home-container {
  min-height: 100vh;
  background: var(--auth-page-bg);
}

/* 头部样式 */
.home-header {
  background: var(--auth-header-bg);
  backdrop-filter: blur(20px);
  box-shadow: 0 2px 20px rgba(22, 163, 74, 0.1);
  position: sticky;
  top: 0;
  z-index: 100;
  border-bottom: 1px solid rgba(22, 163, 74, 0.1);
}

.header-content {
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 24px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  height: 70px;
}

.logo {
  display: flex;
  align-items: center;
  gap: 12px;
}

.logo-image {
  width: 48px;
  height: 48px;
  object-fit: contain;
}

.logo h1 {
  font-size: 28px;
  color: #16a34a;
  margin: 0;
  font-weight: 700;
  background: linear-gradient(135deg, #16a34a, #22c55e);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.nav-menu {
  display: flex;
  align-items: center;
}

.nav-link {
  text-decoration: none;
  color: var(--text-color-primary);
  padding: 12px 24px;
  border-radius: 25px;
  transition: all 0.3s ease;
  font-weight: 500;
  margin-left: 12px;
  position: relative;
  overflow: hidden;
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 16px;
  display: flex;
  align-items: center;
  gap: 6px;
}

.nav-link.language-btn {
  border: 1px solid var(--border-color);
}

.nav-link.theme-btn {
  border: 1px solid var(--border-color);
  padding: 8px 10px;
}

.nav-link:hover {
  background: rgba(22, 163, 74, 0.1);
  color: #16a34a;
  transform: translateY(-2px);
}

.nav-link.primary {
  background: linear-gradient(135deg, #16a34a, #22c55e);
  color: white;
  box-shadow: 0 4px 15px rgba(22, 163, 74, 0.3);
}

.nav-link.primary:hover {
  background: linear-gradient(135deg, #15803d, #16a34a);
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(22, 163, 74, 0.4);
}

/* 主要内容 */
.home-main {
  padding: 60px 0;
}

/* 英雄区域 */
.hero-section {
  display: flex;
  justify-content: center;
  align-items: center;
  max-width: 1200px;
  margin: 0 auto;
  padding: 60px 24px;
  gap: 60px;
  flex-wrap: wrap;
}

.hero-content {
  flex: 1;
  min-width: 400px;
}

.hero-title {
  font-size: 52px;
  color: var(--text-color-primary);
  margin-bottom: 24px;
  line-height: 1.2;
  font-weight: 800;
  background: linear-gradient(135deg, #1f2937, #374151);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.hero-description {
  font-size: 20px;
  color: var(--text-color-secondary);
  margin-bottom: 40px;
  line-height: 1.6;
  font-weight: 400;
}

.hero-actions {
  display: flex;
  gap: 20px;
  flex-wrap: wrap;
}

.btn {
  display: inline-block;
  padding: 16px 32px;
  border-radius: 30px;
  text-decoration: none;
  font-weight: 600;
  font-size: 16px;
  transition: all 0.3s ease;
  position: relative;
  overflow: hidden;
  border: none;
  cursor: pointer;
}

.btn-primary {
  background: linear-gradient(135deg, #16a34a, #22c55e);
  color: white;
  box-shadow: 0 4px 15px rgba(22, 163, 74, 0.3);
}

.btn-primary:hover {
  background: linear-gradient(135deg, #15803d, #16a34a);
  transform: translateY(-3px);
  box-shadow: 0 8px 25px rgba(22, 163, 74, 0.4);
}

.btn-secondary {
  background: transparent;
  color: #16a34a;
  border: 2px solid #16a34a;
  box-shadow: 0 4px 15px rgba(22, 163, 74, 0.1);
}

.btn-secondary:hover {
  background: #16a34a;
  color: white;
  transform: translateY(-3px);
  box-shadow: 0 8px 25px rgba(22, 163, 74, 0.3);
}

.hero-image {
  flex: 1;
  min-width: 400px;
}

.feature-preview {
  display: grid;
  grid-template-columns: 1fr;
  gap: 20px;
}

.preview-card {
  background: var(--card-bg);
  backdrop-filter: blur(10px);
  padding: 24px;
  border-radius: 20px;
  box-shadow: 0 8px 25px rgba(22, 163, 74, 0.1);
  text-align: center;
  transition: all 0.3s ease;
  border: 1px solid rgba(22, 163, 74, 0.1);
}

.preview-card:hover {
  transform: translateY(-8px) scale(1.02);
  box-shadow: 0 15px 35px rgba(22, 163, 74, 0.2);
  border-color: rgba(22, 163, 74, 0.3);
}

.card-icon {
  font-size: 42px;
  margin-bottom: 16px;
}

.preview-card h3 {
  font-size: 18px;
  color: var(--text-color-primary);
  margin-bottom: 8px;
  font-weight: 600;
}

.preview-card p {
  font-size: 14px;
  color: var(--text-color-secondary);
  line-height: 1.5;
}

/* 支持的虚拟化平台 */
.platforms-section {
  max-width: 1200px;
  margin: 100px auto;
  padding: 60px 24px;
  text-align: center;
}

.section-header {
  margin-bottom: 60px;
}

.section-header h2 {
  font-size: 42px;
  color: var(--text-color-primary);
  margin: 0 0 16px 0;
  font-weight: 700;
  background: linear-gradient(135deg, #1f2937, #374151);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.section-header p {
  font-size: 18px;
  color: var(--text-color-secondary);
  margin: 0;
  font-weight: 400;
}

.platforms-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
  gap: 32px;
  margin-top: 60px;
}

/* 统计概况网格：复用 platform-item 的视觉样式，使其与图标卡片一致 */
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 24px;
  margin-top: 36px;
}

.stats-item .platform-icon {
  height: 56px;
}

.stats-value {
  font-size: 28px;
  color: #16a34a;
  font-weight: 700;
  margin-top: 12px;
}

.platform-item {
  background: var(--card-bg);
  backdrop-filter: blur(10px);
  padding: 40px 24px;
  border-radius: 24px;
  box-shadow: 0 8px 25px rgba(22, 163, 74, 0.08);
  transition: all 0.3s ease;
  border: 1px solid rgba(22, 163, 74, 0.1);
  text-align: center;
}

.platform-item:hover {
  transform: translateY(-10px) scale(1.03);
  box-shadow: 0 20px 40px rgba(22, 163, 74, 0.15);
  border-color: rgba(22, 163, 74, 0.3);
}

.platform-icon {
  margin-bottom: 24px;
  display: flex;
  justify-content: center;
  align-items: center;
  height: 80px;
}

.platform-item h3 {
  font-size: 20px;
  color: var(--text-color-primary);
  margin-bottom: 12px;
  font-weight: 600;
}

.platform-item p {
  font-size: 14px;
  color: var(--text-color-secondary);
  line-height: 1.5;
}

/* 系统公告 */
.announcements-section {
  max-width: 1200px;
  margin: 100px auto;
  padding: 60px 24px;
}

.announcements-list {
  display: grid;
  gap: 20px;
  margin-top: 40px;
}

.announcement-item {
  background: var(--card-bg);
  backdrop-filter: blur(10px);
  padding: 24px;
  border-radius: 16px;
  box-shadow: 0 4px 15px rgba(22, 163, 74, 0.05);
  border: 1px solid rgba(22, 163, 74, 0.1);
  transition: all 0.3s ease;
}

.announcement-item:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 25px rgba(22, 163, 74, 0.1);
  border-color: rgba(22, 163, 74, 0.2);
}

.announcement-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
  flex-wrap: wrap;
  gap: 8px;
}

.announcement-header h3 {
  font-size: 18px;
  color: var(--text-color-primary);
  font-weight: 600;
  margin: 0;
  flex: 1;
  min-width: 200px;
}

.announcement-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

.announcement-date {
  font-size: 14px;
  color: var(--text-color-secondary);
  font-weight: 400;
}

.announcement-content {
  font-size: 16px;
  color: var(--text-color-secondary);
  line-height: 1.6;
  margin: 0;
}

/* 富文本内容样式 */
.announcement-content :deep(p) {
  margin: 8px 0;
}

.announcement-content :deep(ul),
.announcement-content :deep(ol) {
  padding-left: 20px;
  margin: 8px 0;
}

.announcement-content :deep(blockquote) {
  border-left: 4px solid #16a34a;
  padding-left: 16px;
  margin: 16px 0;
  font-style: italic;
  background: rgba(22, 163, 74, 0.05);
  padding: 12px 16px;
  border-radius: 4px;
}

.announcement-content :deep(strong) {
  color: var(--text-color-primary);
  font-weight: 600;
}

.announcement-content :deep(code) {
  background: rgba(22, 163, 74, 0.1);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: 14px;
}

/* 页脚 */
.home-footer {
  position: relative;
  background: linear-gradient(160deg, #0d1a12 0%, #0f2318 40%, #111827 100%);
  color: white;
  font-size: 14px;
  margin-top: 100px;
  overflow: hidden;
}

.footer-glow-top {
  position: absolute;
  top: 0;
  left: 50%;
  transform: translateX(-50%);
  width: 600px;
  height: 2px;
  background: linear-gradient(90deg, transparent, #16a34a, #22c55e, #16a34a, transparent);
  box-shadow: 0 0 24px 6px rgba(34, 197, 94, 0.35);
}

.footer-inner {
  max-width: 1200px;
  margin: 0 auto;
  padding: 60px 24px 40px;
  display: grid;
  grid-template-columns: 1fr 2fr;
  gap: 64px;
  align-items: start;
}

/* 品牌栏 */
.footer-brand {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.footer-logo {
  display: flex;
  align-items: center;
  gap: 12px;
}

.footer-logo-img {
  width: 40px;
  height: 40px;
  object-fit: contain;
  filter: drop-shadow(0 0 8px rgba(34, 197, 94, 0.5));
}

.footer-logo-text {
  font-size: 22px;
  font-weight: 700;
  background: linear-gradient(135deg, #22c55e, #4ade80);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.footer-tagline {
  color: rgba(255, 255, 255, 0.5);
  line-height: 1.6;
  font-size: 13px;
  margin: 0;
  max-width: 260px;
}

.footer-github-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 10px 20px;
  border-radius: 24px;
  background: rgba(34, 197, 94, 0.1);
  border: 1px solid rgba(34, 197, 94, 0.25);
  color: #4ade80;
  text-decoration: none;
  font-size: 13px;
  font-weight: 600;
  transition: all 0.3s ease;
  width: fit-content;
  backdrop-filter: blur(8px);
}

.footer-github-btn:hover {
  background: rgba(34, 197, 94, 0.2);
  border-color: rgba(34, 197, 94, 0.5);
  box-shadow: 0 0 18px rgba(34, 197, 94, 0.3);
  transform: translateY(-2px);
  color: #86efac;
}

/* 链接网格 */
.footer-links-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 32px;
}

.footer-col-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 700;
  color: rgba(255, 255, 255, 0.9);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin: 0 0 16px;
}

.footer-col-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: linear-gradient(135deg, #16a34a, #22c55e);
  box-shadow: 0 0 6px rgba(34, 197, 94, 0.7);
  flex-shrink: 0;
}

.footer-link-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.footer-link-list li a {
  display: flex;
  align-items: center;
  gap: 6px;
  color: rgba(255, 255, 255, 0.55);
  text-decoration: none;
  font-size: 13.5px;
  padding: 5px 8px;
  border-radius: 8px;
  transition: all 0.25s ease;
  font-weight: 400;
}

.footer-link-list li a:hover {
  color: #4ade80;
  background: rgba(34, 197, 94, 0.08);
  transform: translateX(4px);
}

.link-arrow {
  font-size: 16px;
  line-height: 1;
  color: #16a34a;
  opacity: 0.6;
  transition: opacity 0.25s;
  flex-shrink: 0;
}

.footer-link-list li a:hover .link-arrow {
  opacity: 1;
}

.more-link {
  font-style: italic;
}

/* 底部版权栏 */
.footer-bottom {
  border-top: 1px solid rgba(255, 255, 255, 0.06);
  background: rgba(0, 0, 0, 0.2);
  backdrop-filter: blur(4px);
  padding: 18px 24px;
}

.footer-bottom-inner {
  max-width: 1200px;
  margin: 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  flex-wrap: wrap;
}

.footer-copyright {
  color: rgba(255, 255, 255, 0.4);
  font-size: 13px;
}

.footer-divider {
  display: inline-block;
  width: 1px;
  height: 14px;
  background: rgba(255, 255, 255, 0.15);
  border-radius: 1px;
}

.footer-bottom-link {
  display: inline-flex;
  align-items: center;
  color: rgba(34, 197, 94, 0.75);
  text-decoration: none;
  font-size: 13px;
  transition: all 0.25s ease;
  font-weight: 500;
}

.footer-bottom-link:hover {
  color: #4ade80;
  text-shadow: 0 0 8px rgba(34, 197, 94, 0.5);
}

/* 响应式调整 */
@media (max-width: 768px) {
  .hero-section {
    flex-direction: column;
    text-align: center;
    gap: 40px;
    padding: 40px 20px;
  }

  .hero-content {
    min-width: unset;
  }

  .hero-title {
    font-size: 36px;
  }

  .hero-description {
    font-size: 18px;
  }

  .hero-actions {
    justify-content: center;
  }

  .hero-image {
    min-width: unset;
    width: 100%;
  }

  .platforms-grid {
    grid-template-columns: 1fr;
    gap: 24px;
  }

  .platform-item {
    padding: 32px 20px;
  }

  .footer-inner {
    grid-template-columns: 1fr;
    gap: 40px;
    padding: 48px 20px 32px;
  }

  .footer-links-grid {
    grid-template-columns: 1fr 1fr;
    gap: 24px;
  }

  .footer-tagline {
    max-width: 100%;
  }

  .header-content {
    padding: 0 20px;
  }

  .section-header h2 {
    font-size: 32px;
  }

  .section-header p {
    font-size: 16px;
  }
}

@media (max-width: 480px) {
  .hero-title {
    font-size: 28px;
  }

  .hero-description {
    font-size: 16px;
  }

  .btn {
    padding: 14px 28px;
    font-size: 15px;
  }

  .platforms-section,
  .announcements-section {
    padding: 40px 20px;
  }

  .footer-links-grid {
    grid-template-columns: 1fr;
  }

  .section-header h2 {
    font-size: 28px;
  }
}
</style>
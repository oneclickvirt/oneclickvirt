<template>
  <div class="home-container">
    <!-- 导航栏 -->
    <header class="home-header">
      <div class="header-content">
        <div class="logo">
          <img
            :src="siteStore.logoSrc"
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

      <!-- 平台概览 -->
      <section class="overview-section">
        <div class="section-header">
          <h2>{{ t('home.platformOverview.title') }}</h2>
          <p>{{ t('home.platformOverview.description') }}</p>
        </div>
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

      <!-- 支持的虚拟化平台 -->
      <section class="platforms-section">
        <div class="section-header">
          <h2>{{ t('home.platforms.title') }}</h2>
          <p>{{ t('home.platforms.description') }}</p>
        </div>
        <div class="platforms-grid">
          <a
            class="platform-item platform-link"
            href="https://github.com/oneclickvirt/pve"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon pve-icon">
              <img
                src="@/assets/images/proxmox.png"
                alt="Proxmox VE"
                width="60"
                height="60"
              >
            </div>
            <h3>Proxmox VE</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/pve
            </span>
          </a>

          <a
            class="platform-item platform-link"
            href="https://github.com/oneclickvirt/incus"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon incus-icon">
              <img
                src="@/assets/images/incus.png"
                alt="Incus"
                width="60"
                height="60"
              >
            </div>
            <h3>Incus</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/incus
            </span>
          </a>

          <a
            class="platform-item platform-link"
            href="https://github.com/oneclickvirt/docker"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon docker-icon">
              <img
                src="@/assets/images/docker.png"
                alt="Docker"
                width="60"
                height="60"
              >
            </div>
            <h3>Docker</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/docker
            </span>
          </a>

          <a
            class="platform-item platform-link"
            href="https://github.com/oneclickvirt/lxd"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon lxd-icon">
              <img
                src="@/assets/images/lxd.png"
                alt="LXD"
                width="60"
                height="60"
              >
            </div>
            <h3>LXD</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/lxd
            </span>
          </a>

          <a
            class="platform-item platform-link"
            href="https://github.com/oneclickvirt/podman"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon">
              <img
                src="@/assets/images/podman.svg"
                alt="Podman"
                width="60"
                height="60"
              >
            </div>
            <h3>Podman</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/podman
            </span>
          </a>

          <a
            class="platform-item platform-link"
            href="https://github.com/oneclickvirt/containerd"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon">
              <img
                src="@/assets/images/containerd.svg"
                alt="Containerd"
                width="60"
                height="60"
              >
            </div>
            <h3>Containerd</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/containerd
            </span>
          </a>
        </div>

        <!-- 即将支持（居中展示） -->
        <div class="platforms-wip-row">
          <a
            class="platform-item platform-link platform-wip"
            href="https://github.com/oneclickvirt/qemu"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon">
              <img
                src="@/assets/images/qemu.svg"
                alt="QEMU"
                width="60"
                height="60"
              >
            </div>
            <h3>QEMU</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/qemu
            </span>
          </a>

          <a
            class="platform-item platform-link platform-wip"
            href="https://github.com/oneclickvirt/kubevirt"
            target="_blank"
            rel="noopener noreferrer"
          >
            <div class="platform-icon">
              <img
                src="@/assets/images/KubeVirt.png"
                alt="KubeVirt"
                width="60"
                height="60"
              >
            </div>
            <h3>KubeVirt</h3>
            <span class="platform-link-hint">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
              oneclickvirt/kubevirt
            </span>
          </a>
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
              :src="siteStore.logoSrc"
              alt="OneClickVirt Logo"
              class="footer-logo-img"
            >
            <span class="footer-logo-text">{{ siteStore.displaySiteName }}</span>
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
          <span class="footer-copyright">&copy; 2026 {{ siteStore.displaySiteName }}. {{ t('home.footer.allRightsReserved') }}</span>
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
          <template v-if="serverVersion">
            <span class="footer-divider" />
            <span class="footer-version-tag">{{ t('home.footer.serverVersion') }} {{ serverVersion }}</span>
          </template>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getPublicAnnouncements, getPublicStats, getServerVersion } from '@/api/public'
import { checkSystemInit } from '@/api/init'
import { ElTag, ElMessage } from 'element-plus'
import { Operation, Sunny, Moon } from '@element-plus/icons-vue'
import { useLanguageStore } from '@/pinia/modules/language'
import { useThemeStore } from '@/pinia/modules/theme'
import { useSiteStore } from '@/pinia/modules/site'

const router = useRouter()
const { t, locale } = useI18n()
const languageStore = useLanguageStore()
const themeStore = useThemeStore()
const siteStore = useSiteStore()
const announcements = ref([])
// 统计数据
const usersCount = ref(null)
const nodesCount = ref(null)
const containersCount = ref(null)
const vmsCount = ref(null)
const serverVersion = ref('')

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
    if (response && (response.code === 0 || response.code === 200) && response.data && response.data.needInit === true) {
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
  // 获取服务器版本信息
  getServerVersion().then(res => {
    if (res && (res.code === 0 || res.code === 200) && res.data?.server_version) {
      serverVersion.value = res.data.server_version
    }
  }).catch(() => {})
})
</script>

<style src="./home.css" scoped></style>
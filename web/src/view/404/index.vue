<template>
  <div class="not-found-container">
    <div class="not-found-bg" />
    <div class="not-found-content">
      <div class="error-illustration">
        <div class="error-shapes">
          <div class="shape shape-1" />
          <div class="shape shape-2" />
          <div class="shape shape-3" />
        </div>
        <div class="error-code">
          404
        </div>
      </div>
      <h1 class="error-title">
        {{ t('notFound.title') }}
      </h1>
      <p class="error-message">
        {{ t('notFound.message') }}
      </p>
      <div class="actions">
        <el-button
          type="primary"
          size="large"
          round
          @click="goHome"
        >
          {{ t('notFound.goHome') }}
        </el-button>
        <el-button
          size="large"
          round
          @click="goBack"
        >
          {{ t('notFound.goBack') }}
        </el-button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

const router = useRouter()
const { t } = useI18n()

const goHome = () => {
  router.push('/')
}

const goBack = () => {
  router.go(-1)
}
</script>

<style scoped>
.not-found-container {
  min-height: 100vh;
  min-height: 100dvh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--auth-page-bg);
  padding: 20px;
  padding-bottom: calc(20px + env(safe-area-inset-bottom));
  position: relative;
  overflow: hidden;
}

.not-found-bg {
  position: absolute;
  inset: 0;
  background-image:
    radial-gradient(circle at 30% 20%, var(--accent-soft-bg) 0%, transparent 50%),
    radial-gradient(circle at 70% 80%, color-mix(in srgb, var(--info-color) 6%, transparent) 0%, transparent 50%);
}

.not-found-content {
  text-align: center;
  max-width: 520px;
  position: relative;
  z-index: 1;
}

.error-illustration {
  position: relative;
  margin-bottom: 24px;
  display: inline-block;
}

.error-shapes {
  position: absolute;
  inset: -40px;
  z-index: 0;
}

.shape {
  position: absolute;
  border-radius: 50%;
  opacity: var(--decorative-shape-opacity);
}

.shape-1 {
  width: 180px;
  height: 180px;
  background: var(--primary-color-light);
  top: -20px;
  left: -30px;
  animation: float 6s ease-in-out infinite;
}

.shape-2 {
  width: 120px;
  height: 120px;
  background: var(--info-color);
  bottom: -10px;
  right: -20px;
  animation: float 8s ease-in-out infinite reverse;
}

.shape-3 {
  width: 80px;
  height: 80px;
  background: var(--warning-color);
  top: 10px;
  right: 20px;
  animation: float 5s ease-in-out infinite 1s;
}

@keyframes float {
  0%, 100% { transform: translateY(0) scale(1); }
  50% { transform: translateY(-12px) scale(1.05); }
}

.error-code {
  font-size: 140px;
  font-weight: 800;
  background: linear-gradient(135deg, var(--primary-color), var(--primary-color-light), var(--primary-color-lighter));
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  position: relative;
  z-index: 2;
  line-height: 1;
  letter-spacing: -4px;
}

.error-title {
  font-size: 28px;
  color: var(--text-color-primary);
  margin: 0 0 12px;
  font-weight: 700;
}

.error-message {
  font-size: 16px;
  color: var(--text-color-secondary);
  margin: 0 0 28px;
  line-height: 1.6;
}

.actions {
  display: flex;
  gap: 12px;
  justify-content: center;
}

:deep(.el-button--primary) {
  background: var(--primary-color);
  border-color: var(--primary-color);
}

:deep(.el-button--primary:hover) {
  background: var(--primary-color-dark);
  border-color: var(--primary-color-dark);
}

@media (max-width: 640px) {
  .not-found-container {
    padding: 14px;
    padding-bottom: calc(14px + env(safe-area-inset-bottom));
  }

  .error-code {
    font-size: 100px;
  }

  .error-title {
    font-size: 24px;
  }

  .error-message {
    font-size: 15px;
  }

  .actions {
    flex-wrap: wrap;
  }

  .shape-1 { width: 120px; height: 120px; }
  .shape-2 { width: 80px; height: 80px; }
  .shape-3 { width: 50px; height: 50px; }
}
</style>

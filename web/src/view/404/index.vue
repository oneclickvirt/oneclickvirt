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
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #f0fdf4 0%, #ecfdf5 50%, #f0f9ff 100%);
  padding: 24px;
  position: relative;
  overflow: hidden;
}

.not-found-bg {
  position: absolute;
  inset: 0;
  background-image:
    radial-gradient(circle at 30% 20%, rgba(34, 197, 94, 0.06) 0%, transparent 50%),
    radial-gradient(circle at 70% 80%, rgba(59, 130, 246, 0.05) 0%, transparent 50%);
}

.not-found-content {
  text-align: center;
  max-width: 520px;
  position: relative;
  z-index: 1;
}

.error-illustration {
  position: relative;
  margin-bottom: 32px;
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
  opacity: 0.15;
}

.shape-1 {
  width: 180px;
  height: 180px;
  background: #22c55e;
  top: -20px;
  left: -30px;
  animation: float 6s ease-in-out infinite;
}

.shape-2 {
  width: 120px;
  height: 120px;
  background: #3b82f6;
  bottom: -10px;
  right: -20px;
  animation: float 8s ease-in-out infinite reverse;
}

.shape-3 {
  width: 80px;
  height: 80px;
  background: #f59e0b;
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
  background: linear-gradient(135deg, #16a34a, #22c55e, #4ade80);
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
  color: #111827;
  margin: 0 0 12px;
  font-weight: 700;
}

.error-message {
  font-size: 16px;
  color: #6b7280;
  margin: 0 0 36px;
  line-height: 1.6;
}

.actions {
  display: flex;
  gap: 12px;
  justify-content: center;
}

:deep(.el-button--primary) {
  background: #16a34a;
  border-color: #16a34a;
}

:deep(.el-button--primary:hover) {
  background: #15803d;
  border-color: #15803d;
}

@media (max-width: 640px) {
  .error-code {
    font-size: 100px;
  }

  .error-title {
    font-size: 24px;
  }

  .error-message {
    font-size: 15px;
  }

  .shape-1 { width: 120px; height: 120px; }
  .shape-2 { width: 80px; height: 80px; }
  .shape-3 { width: 50px; height: 50px; }
}
</style>
<template>
  <div class="not-found-container">
    <div class="not-found-content">
      <div class="error-illustration">
        <div class="error-code">404</div>
        <div class="error-circle" />
      </div>
      <h1 class="error-title">
        {{ t('notFound.title') }}
      </h1>
      <p class="error-message">
        {{ t('notFound.message') }}
      </p>
      <div class="actions">
        <button
          class="btn btn-primary"
          @click="goHome"
        >
          {{ t('notFound.goHome') }}
        </button>
        <button
          class="btn btn-secondary"
          @click="goBack"
        >
          {{ t('notFound.goBack') }}
        </button>
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
  background: linear-gradient(135deg, #f0fdf4 0%, #ecfdf5 100%);
  padding: 24px;
}

.not-found-content {
  text-align: center;
  max-width: 600px;
}

.error-illustration {
  position: relative;
  margin-bottom: 40px;
  display: inline-block;
}

.error-code {
  font-size: 140px;
  font-weight: 800;
  background: linear-gradient(135deg, #16a34a, #22c55e);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  position: relative;
  z-index: 2;
  line-height: 1;
}

.error-circle {
  position: absolute;
  width: 200px;
  height: 200px;
  border-radius: 50%;
  background: rgba(22, 163, 74, 0.1);
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 1;
  animation: pulse 2s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% {
    transform: translate(-50%, -50%) scale(1);
    opacity: 0.5;
  }
  50% {
    transform: translate(-50%, -50%) scale(1.1);
    opacity: 0.8;
  }
}

.error-title {
  font-size: 32px;
  color: #1f2937;
  margin: 0 0 16px 0;
  font-weight: 700;
}

.error-message {
  font-size: 18px;
  color: #6b7280;
  margin: 0 0 40px 0;
  line-height: 1.6;
}

.actions {
  display: flex;
  gap: 16px;
  justify-content: center;
  flex-wrap: wrap;
}

.btn {
  padding: 12px 32px;
  border-radius: 25px;
  font-size: 16px;
  font-weight: 500;
  cursor: pointer;
  border: none;
  transition: all 0.3s ease;
  text-decoration: none;
  display: inline-block;
}

.btn-primary {
  background: linear-gradient(135deg, #16a34a, #22c55e);
  color: white;
  box-shadow: 0 4px 15px rgba(22, 163, 74, 0.3);
}

.btn-primary:hover {
  background: linear-gradient(135deg, #15803d, #16a34a);
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(22, 163, 74, 0.4);
}

.btn-secondary {
  background: white;
  color: #374151;
  border: 1px solid #e5e7eb;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
}

.btn-secondary:hover {
  background: rgba(22, 163, 74, 0.05);
  color: #16a34a;
  border-color: #16a34a;
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(22, 163, 74, 0.15);
}

@media (max-width: 768px) {
  .error-code {
    font-size: 100px;
  }

  .error-circle {
    width: 150px;
    height: 150px;
  }

  .error-title {
    font-size: 28px;
  }

  .error-message {
    font-size: 16px;
  }

  .btn {
    padding: 10px 24px;
    font-size: 14px;
  }
}
</style>
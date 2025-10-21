<template>
  <div class="not-found-container">
    <div class="not-found-content">
      <div class="error-code">
        404
      </div>
      <h1>{{ t('notFound.title') }}</h1>
      <p>{{ t('notFound.message') }}</p>
      <div class="actions">
        <el-button
          type="primary"
          @click="goHome"
        >
          {{ t('notFound.goHome') }}
        </el-button>
        <el-button @click="goBack">
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
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
}

.not-found-content {
  text-align: center;
  color: white;
}

.error-code {
  font-size: 120px;
  font-weight: bold;
  margin-bottom: 20px;
  text-shadow: 2px 2px 4px rgba(0, 0, 0, 0.3);
}

h1 {
  font-size: 36px;
  margin: 0 0 20px 0;
  font-weight: 600;
}

p {
  font-size: 18px;
  margin: 0 0 40px 0;
  opacity: 0.9;
}

.actions {
  display: flex;
  gap: 20px;
  justify-content: center;
}
</style>
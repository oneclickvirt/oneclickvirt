<template>
  <div class="checkin-container">
    <el-card>
      <template #header>
        <span>{{ t('user.checkin.title') }}</span>
      </template>

      <div v-loading="loadingInstances">
        <template v-if="instances.length > 0">
          <div
            v-loading="loadingStats"
            class="checkin-stats"
          >
        <el-row :gutter="12">
          <el-col
            :xs="12"
            :sm="6"
          >
            <el-statistic
              :title="t('user.checkin.totalCheckins')"
              :value="stats.totalCheckins"
            />
          </el-col>
          <el-col
            :xs="12"
            :sm="6"
          >
            <el-statistic
              :title="t('user.checkin.currentStreak')"
              :value="stats.currentStreak"
              :suffix="t('user.checkin.days')"
            />
          </el-col>
          <el-col
            :xs="12"
            :sm="6"
          >
            <el-statistic
              :title="t('user.checkin.longestStreak')"
              :value="stats.longestStreak"
              :suffix="t('user.checkin.days')"
            />
          </el-col>
          <el-col
            :xs="12"
            :sm="6"
          >
            <el-statistic
              :title="t('user.checkin.totalRenewalDays')"
              :value="stats.totalRenewalDays"
              :suffix="t('user.checkin.days')"
            />
          </el-col>
        </el-row>
      </div>

      <!-- 签到操作 -->
      <el-form
        label-width="120px"
        style="max-width: 600px; margin-bottom: 30px;"
      >
        <el-form-item :label="t('user.checkin.selectInstance')">
          <el-select
            v-model="selectedInstanceId"
            :placeholder="t('user.checkin.selectInstance')"
            @change="resetChallenge"
          >
            <el-option
              v-for="inst in instances"
              :key="inst.id"
              :label="inst.name"
              :value="inst.id"
            />
          </el-select>
        </el-form-item>

        <!-- 获取挑战 -->
        <el-form-item>
          <el-button
            type="primary"
            :disabled="!selectedInstanceId"
            :loading="gettingChallenge"
            @click="getChallenge"
          >
            {{ t('user.checkin.getCode') }}
          </el-button>
        </el-form-item>

        <!-- 内置验证码方式 -->
        <template v-if="challengeData && challengeData.method === 'captcha'">
          <el-form-item :label="t('user.checkin.inputCode')">
            <el-input
              v-model="inputCode"
              style="width: 200px; margin-right: 10px;"
            />
            <el-button
              type="success"
              :loading="checkingIn"
              @click="doCheckin"
            >
              {{ t('user.checkin.checkin') }}
            </el-button>
          </el-form-item>
          <el-form-item>
            <el-tag type="info">
              {{ challengeData.code }}
            </el-tag>
          </el-form-item>
        </template>

        <!-- Turnstile / reCAPTCHA / hCaptcha -->
        <template v-if="challengeData && ['turnstile', 'recaptcha', 'hcaptcha'].includes(challengeData.method)">
          <el-form-item :label="t('user.checkin.verification')">
            <div
              ref="captchaContainer"
              class="captcha-widget"
            />
            <div
              v-if="!captchaLoaded"
              class="captcha-loading"
            >
              <el-text type="info">
                {{ t('user.checkin.loadingCaptcha') }}
              </el-text>
            </div>
          </el-form-item>
          <el-form-item>
            <el-button
              type="success"
              :loading="checkingIn"
              :disabled="!captchaToken"
              @click="doCheckin"
            >
              {{ t('user.checkin.checkin') }}
            </el-button>
          </el-form-item>
        </template>

        <!-- PoW -->
        <template v-if="challengeData && challengeData.method === 'pow'">
          <el-form-item :label="t('user.checkin.powStatus')">
            <div
              v-if="powComputing"
              class="pow-computing"
            >
              <el-icon class="is-loading">
                <Loading />
              </el-icon>
              <el-text style="margin-left: 8px;">
                {{ t('user.checkin.powComputing') }}
              </el-text>
            </div>
            <div v-else-if="powNonce">
              <el-tag type="success">
                {{ t('user.checkin.powSolved') }}
              </el-tag>
            </div>
            <div v-else>
              <el-button
                type="warning"
                @click="solvePow"
              >
                {{ t('user.checkin.powStart') }}
              </el-button>
            </div>
          </el-form-item>
          <el-form-item v-if="powNonce">
            <el-button
              type="success"
              :loading="checkingIn"
              @click="doCheckin"
            >
              {{ t('user.checkin.checkin') }}
            </el-button>
          </el-form-item>
        </template>
      </el-form>

      <!-- 签到记录 -->
      <el-divider />
      <h3>{{ t('user.checkin.records') }}</h3>
      <el-table
        v-loading="loadingRecords"
        :data="records"
        stripe
      >
        <el-table-column
          prop="instanceId"
          :label="t('user.checkin.instanceName')"
          width="120"
        >
          <template #default="{ row }">
            {{ formatInstanceName(row.instanceId) }}
          </template>
        </el-table-column>
        <el-table-column
          prop="method"
          :label="t('user.checkin.method')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag size="small">
              {{ row.method }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="renewalDays"
          :label="t('user.checkin.renewalDays')"
          width="100"
        />
        <el-table-column
          :label="t('user.checkin.oldExpireAt')"
          width="180"
        >
          <template #default="{ row }">
            {{ formatDate(row.oldExpireAt) }}
          </template>
        </el-table-column>
        <el-table-column
          :label="t('user.checkin.newExpireAt')"
          width="180"
        >
          <template #default="{ row }">
            {{ formatDate(row.newExpireAt) }}
          </template>
        </el-table-column>
        <el-table-column
          :label="t('user.checkin.checkinTime')"
          width="180"
        >
          <template #default="{ row }">
            {{ formatDate(row.createdAt) }}
          </template>
        </el-table-column>
      </el-table>
      <el-pagination
        v-if="total > pageSize"
        style="margin-top: 16px; justify-content: flex-end;"
        :current-page="page"
        :page-size="pageSize"
        :total="total"
        layout="total, prev, pager, next"
        @current-change="handlePageChange"
      />
        </template>
        <el-empty
          v-else-if="!loadingInstances"
          description="当前没有可签到续期的实例；只有实例所属节点已启用签到续期时，这里才会显示操作界面。"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { Loading } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { generateCheckinCode, doCheckin as doCheckinApi, getCheckinRecords, getCheckinStats, getEligibleCheckinInstances } from '@/api/features'

const { t } = useI18n()

const instances = ref([])
const selectedInstanceId = ref(null)
const challengeData = ref(null)
const inputCode = ref('')
const captchaToken = ref('')
const captchaLoaded = ref(false)
const powNonce = ref('')
const powComputing = ref(false)
const gettingChallenge = ref(false)
const checkingIn = ref(false)
const loadingStats = ref(false)
const loadingInstances = ref(true)
const stats = ref({
  totalCheckins: 0,
  currentStreak: 0,
  longestStreak: 0,
  totalRenewalDays: 0,
  thisMonthCheckins: 0,
  lastCheckinDate: ''
})
const records = ref([])
const loadingRecords = ref(false)
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const captchaContainer = ref(null)
const activeWorker = ref(null)

function formatDate(dateStr) {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

function formatInstanceName(instanceId) {
  const inst = instances.value.find(item => item.id === instanceId)
  return inst?.name || `#${instanceId}`
}

function resetChallenge() {
  challengeData.value = null
  inputCode.value = ''
  captchaToken.value = ''
  powNonce.value = ''
  powComputing.value = false
  captchaLoaded.value = false
}

async function fetchInstances() {
  loadingInstances.value = true
  try {
    const res = await getEligibleCheckinInstances()
    if (res.code === 200) {
      const nextInstances = Array.isArray(res.data) ? res.data : []
      instances.value = nextInstances
      if (!instances.value.some(item => item.id === selectedInstanceId.value)) {
        selectedInstanceId.value = null
        resetChallenge()
      }
    }
  } catch (e) {
    console.error('获取可签到续期实例失败:', e)
    instances.value = []
    selectedInstanceId.value = null
    resetChallenge()
  } finally {
    loadingInstances.value = false
  }
}

async function fetchStats() {
  loadingStats.value = true
  try {
    const res = await getCheckinStats()
    if (res.code === 200) {
      stats.value = {
        ...stats.value,
        ...(res.data || {})
      }
    }
  } catch (e) {
    console.error('获取签到统计失败:', e)
  } finally {
    loadingStats.value = false
  }
}

async function getChallenge() {
  resetChallenge()
  gettingChallenge.value = true
  try {
    const res = await generateCheckinCode(selectedInstanceId.value)
    if (res.code === 200) {
      challengeData.value = res.data
      ElMessage.success(t('user.checkin.codeSent'))
      // 加载第三方验证组件
      if (['turnstile', 'recaptcha', 'hcaptcha'].includes(res.data.method)) {
        await nextTick()
        loadCaptchaWidget(res.data.method, res.data.siteKey)
      }
    }
  } catch (e) {
    ElMessage.error(e?.message || t('user.checkin.getChallengeError'))
  } finally {
    gettingChallenge.value = false
  }
}

function loadCaptchaWidget(method, siteKey) {
  if (!captchaContainer.value) return

  const containerId = 'checkin-captcha-' + Date.now()
  captchaContainer.value.id = containerId
  captchaContainer.value.innerHTML = ''

  if (method === 'turnstile') {
    // 注册 onTurnstileLoaded 回调（Cloudflare SDK加载完成后调用）
    const renderTurnstile = () => {
      if (window.turnstile) {
        window.turnstile.render('#' + containerId, {
          sitekey: siteKey,
          callback: (token) => { captchaToken.value = token; captchaLoaded.value = true }
        })
        captchaLoaded.value = true
      }
    }
    window.onTurnstileLoaded = renderTurnstile
    loadScript('https://challenges.cloudflare.com/turnstile/v0/api.js?onload=onTurnstileLoaded&render=explicit', renderTurnstile)
  } else if (method === 'recaptcha') {
    const renderRecaptcha = () => {
      if (window.grecaptcha) {
        window.grecaptcha.render(containerId, {
          sitekey: siteKey,
          callback: (token) => { captchaToken.value = token; captchaLoaded.value = true }
        })
        captchaLoaded.value = true
      }
    }
    window.onRecaptchaLoaded = renderRecaptcha
    loadScript('https://www.google.com/recaptcha/api.js?onload=onRecaptchaLoaded&render=explicit', renderRecaptcha)
  } else if (method === 'hcaptcha') {
    loadScript('https://js.hcaptcha.com/1/api.js?onload=onHcaptchaLoaded&render=explicit', () => {
      if (window.hcaptcha) {
        window.hcaptcha.render(containerId, {
          sitekey: siteKey,
          callback: (token) => { captchaToken.value = token; captchaLoaded.value = true }
        })
        captchaLoaded.value = true
      }
    })
  }
}

function loadScript(src, onload) {
  // 避免重复加载
  if (document.querySelector(`script[src="${src}"]`)) {
    if (onload) onload()
    return
  }
  const script = document.createElement('script')
  script.src = src
  script.async = true
  if (onload) script.onload = onload
  document.head.appendChild(script)
}

async function solvePow() {
  if (!challengeData.value) return
  powComputing.value = true
  const { challenge, difficulty } = challengeData.value
  const prefix = '0'.repeat(difficulty)

  // 在Web Worker中计算，避免阻塞UI
  const workerCode = `
    self.onmessage = async function(e) {
      const { challenge, prefix } = e.data;
      let nonce = 0;
      while (true) {
        const data = new TextEncoder().encode(challenge + nonce.toString());
        const hashBuffer = await crypto.subtle.digest('SHA-256', data);
        const hashArray = Array.from(new Uint8Array(hashBuffer));
        const hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
        if (hashHex.startsWith(prefix)) {
          self.postMessage({ nonce: nonce.toString(), hash: hashHex });
          return;
        }
        nonce++;
        if (nonce % 10000 === 0) {
          // 让出控制权
          await new Promise(r => setTimeout(r, 0));
        }
      }
    };
  `
  const blob = new Blob([workerCode], { type: 'application/javascript' })
  const blobUrl = URL.createObjectURL(blob)
  const worker = new Worker(blobUrl)
  activeWorker.value = worker

  worker.onmessage = (e) => {
    powNonce.value = e.data.nonce
    powComputing.value = false
    worker.terminate()
    URL.revokeObjectURL(blobUrl)
    activeWorker.value = null
  }
  worker.onerror = () => {
    powComputing.value = false
    worker.terminate()
    URL.revokeObjectURL(blobUrl)
    activeWorker.value = null
    ElMessage.error(t('user.checkin.powFailed'))
  }
  worker.postMessage({ challenge, prefix })
}

async function doCheckin() {
  if (!challengeData.value || !selectedInstanceId.value) return
  checkingIn.value = true
  try {
    const data = { instanceId: selectedInstanceId.value }

    if (challengeData.value.method === 'captcha') {
      data.code = inputCode.value
    } else if (['turnstile', 'recaptcha', 'hcaptcha'].includes(challengeData.value.method)) {
      data.token = captchaToken.value
    } else if (challengeData.value.method === 'pow') {
      data.challenge = challengeData.value.challenge
      data.nonce = powNonce.value
    }

    const res = await doCheckinApi(data)
    if (res.code === 200) {
      ElMessage.success(t('user.checkin.checkinSuccess'))
      resetChallenge()
      fetchRecords()
      fetchStats()
    }
  } catch (e) {
    ElMessage.error(e?.message || t('user.checkin.checkinFailed'))
  } finally {
    checkingIn.value = false
  }
}

async function fetchRecords() {
  loadingRecords.value = true
  try {
    const res = await getCheckinRecords({ page: page.value, pageSize: pageSize.value })
    if (res.code === 200) {
      records.value = res.data?.list || []
      total.value = res.data?.total || 0
    }
  } catch (e) {
    console.error('获取签到记录失败:', e)
  } finally {
    loadingRecords.value = false
  }
}

function handlePageChange(p) {
  page.value = p
  fetchRecords()
}

onMounted(async () => {
  await fetchInstances()
  if (instances.value.length > 0) {
    fetchStats()
    fetchRecords()
  }
})

onUnmounted(() => {
  // 组件卸载时终止尚在运行的PoW Worker，防止内存泄漏
  if (activeWorker.value) {
    activeWorker.value.terminate()
    activeWorker.value = null
  }
})
</script>

<style scoped>
.checkin-container { padding: 20px; }
.checkin-stats { margin-bottom: 24px; }
.captcha-widget { min-height: 65px; }
.captcha-loading { color: #909399; font-size: 14px; }
.pow-computing { display: flex; align-items: center; }
</style>

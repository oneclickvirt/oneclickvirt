<template>
  <div
    v-loading="loading"
    class="checkin-config-tab"
  >
    <el-form
      :model="checkinForm"
      label-width="160px"
      class="server-form"
    >
      <el-form-item :label="$t('admin.providers.checkinEnabled')">
        <el-switch
          v-model="checkinForm.enabled"
          :active-text="$t('common.yes')"
          :inactive-text="$t('common.no')"
        />
      </el-form-item>
      <div
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 160px;"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.checkinEnabledTip') }}
        </el-text>
      </div>

      <template v-if="checkinForm.enabled">
        <el-form-item :label="$t('admin.providers.checkinDefaultExpireDays')">
          <el-input-number
            v-model="checkinForm.defaultExpireDays"
            :min="1"
            :max="365"
            :step="1"
            style="width: 200px"
          />
        </el-form-item>
        <div
          class="form-tip"
          style="margin-top: -10px; margin-bottom: 15px; margin-left: 160px;"
        >
          <el-text
            size="small"
            type="info"
          >
            {{ $t('admin.providers.checkinDefaultExpireDaysTip') }}
          </el-text>
        </div>

        <el-form-item :label="$t('admin.providers.checkinRenewalDays')">
          <el-input-number
            v-model="checkinForm.renewalDays"
            :min="1"
            :max="365"
            :step="1"
            style="width: 200px"
          />
        </el-form-item>
        <div
          class="form-tip"
          style="margin-top: -10px; margin-bottom: 15px; margin-left: 160px;"
        >
          <el-text
            size="small"
            type="info"
          >
            {{ $t('admin.providers.checkinRenewalDaysTip') }}
          </el-text>
        </div>

        <el-form-item :label="$t('admin.providers.checkinMaxExpireDays')">
          <el-input-number
            v-model="checkinForm.maxExpireDays"
            :min="1"
            :max="3650"
            :step="1"
            style="width: 200px"
          />
        </el-form-item>
        <div
          class="form-tip"
          style="margin-top: -10px; margin-bottom: 15px; margin-left: 160px;"
        >
          <el-text
            size="small"
            type="info"
          >
            {{ $t('admin.providers.checkinMaxExpireDaysTip') }}
          </el-text>
        </div>

        <el-form-item :label="$t('admin.providers.checkinOverdueAction')">
          <el-select
            v-model="checkinForm.overdueAction"
            style="width: 200px"
          >
            <el-option
              :label="$t('admin.providers.checkinActionStop')"
              value="stop"
            />
            <el-option
              :label="$t('admin.providers.checkinActionDelete')"
              value="delete"
            />
          </el-select>
        </el-form-item>
        <div
          class="form-tip"
          style="margin-top: -10px; margin-bottom: 15px; margin-left: 160px;"
        >
          <el-text
            size="small"
            type="info"
          >
            {{ $t('admin.providers.checkinOverdueActionTip') }}
          </el-text>
        </div>

        <el-form-item :label="$t('admin.providers.checkinMethod')">
          <el-select
            v-model="checkinForm.checkinMethod"
            style="width: 200px"
          >
            <el-option
              label="Captcha"
              value="captcha"
            />
            <el-option
              label="Cloudflare Turnstile"
              value="turnstile"
            />
            <el-option
              label="Google reCAPTCHA"
              value="recaptcha"
            />
            <el-option
              label="hCaptcha"
              value="hcaptcha"
            />
            <el-option
              label="PoW"
              value="pow"
            />
          </el-select>
        </el-form-item>

        <!-- 第三方验证配置 -->
        <template v-if="['turnstile', 'recaptcha', 'hcaptcha'].includes(checkinForm.checkinMethod)">
          <el-form-item :label="$t('admin.providers.checkinSiteKey')">
            <el-input
              v-model="checkinForm.captchaSiteKey"
              :placeholder="$t('admin.providers.checkinSiteKeyTip')"
              style="width: 400px"
            />
          </el-form-item>
          <el-form-item :label="$t('admin.providers.checkinSecretKey')">
            <el-input
              v-model="checkinForm.captchaSecretKey"
              type="password"
              show-password
              :placeholder="$t('admin.providers.checkinSecretKeyTip')"
              style="width: 400px"
            />
          </el-form-item>
        </template>

        <!-- PoW 配置 -->
        <template v-if="checkinForm.checkinMethod === 'pow'">
          <el-form-item :label="$t('admin.providers.checkinPowDifficulty')">
            <el-input-number
              v-model="checkinForm.powDifficulty"
              :min="1"
              :max="8"
              :step="1"
              style="width: 200px"
            />
          </el-form-item>
          <div
            class="form-tip"
            style="margin-top: -10px; margin-bottom: 15px; margin-left: 160px;"
          >
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.checkinPowDifficultyTip') }}
            </el-text>
          </div>
        </template>
      </template>

      <el-form-item>
        <el-button
          type="primary"
          :loading="saving"
          @click="handleSave"
        >
          {{ $t('common.save') }}
        </el-button>
      </el-form-item>
    </el-form>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getCheckinConfig, updateCheckinConfig } from '@/api/features'

const { t } = useI18n()

const props = defineProps({
  providerId: {
    type: [Number, String],
    default: null
  }
})

const loading = ref(false)
const saving = ref(false)
const checkinForm = ref({
  enabled: false,
  defaultExpireDays: 7,
  renewalDays: 7,
  maxExpireDays: 30,
  overdueAction: 'stop',
  checkinMethod: 'captcha',
  captchaSiteKey: '',
  captchaSecretKey: '',
  powDifficulty: 4
})

const loadConfig = async () => {
  if (!props.providerId) return
  loading.value = true
  try {
    const res = await getCheckinConfig(props.providerId)
    if (res.data) {
      checkinForm.value = {
        enabled: res.data.enabled ?? false,
        defaultExpireDays: res.data.defaultExpireDays ?? 7,
        renewalDays: res.data.renewalDays ?? 7,
        maxExpireDays: res.data.maxExpireDays ?? 30,
        overdueAction: res.data.overdueAction || 'stop',
        checkinMethod: res.data.checkinMethod || 'captcha',
        captchaSiteKey: res.data.captchaSiteKey || '',
        captchaSecretKey: res.data.captchaSecretKey || '',
        powDifficulty: res.data.powDifficulty || 4
      }
    }
  } catch {
    // Config doesn't exist yet, use defaults
  } finally {
    loading.value = false
  }
}

const handleSave = async () => {
  if (!props.providerId) return
  saving.value = true
  try {
    await updateCheckinConfig(props.providerId, checkinForm.value)
    ElMessage.success(t('common.saveSuccess'))
  } catch (error) {
    console.error('Save checkin config failed:', error)
    ElMessage.error(error?.message || t('common.saveFailed'))
  } finally {
    saving.value = false
  }
}

watch(() => props.providerId, (val) => {
  if (val) loadConfig()
}, { immediate: true })
</script>

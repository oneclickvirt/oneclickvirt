<template>
  <div class="kyc-container">
    <el-card>
      <template #header>
        <span>{{ t('user.kyc.title') }}</span>
      </template>

      <!-- 已通过 -->
      <el-result v-if="kycStatus === 'approved'" icon="success" :title="t('user.kyc.statusApproved')" :sub-title="t('user.kyc.alreadyVerified')" />

      <!-- 审核中(手动) -->
      <el-result v-else-if="kycStatus === 'pending' && kycRecord?.method === 'manual'" icon="warning" :title="t('user.kyc.statusPending')" :sub-title="t('user.kyc.pendingReview')" />

      <!-- 审核中(支付宝) — 可查询结果 -->
      <div v-else-if="kycStatus === 'pending' && kycRecord?.method === 'alipay'">
        <el-result icon="warning" :title="t('user.kyc.statusPending')" :sub-title="t('user.kyc.alipayPendingTip')" />
        <div style="text-align: center; margin-top: 16px;">
          <el-button type="primary" :loading="queryLoading" @click="handleQueryAlipay">
            {{ t('user.kyc.queryAlipayResult') }}
          </el-button>
        </div>
      </div>

      <!-- 已拒绝 -->
      <div v-else-if="kycStatus === 'rejected'">
        <el-alert type="error" :title="t('user.kyc.statusRejected')" :description="kycRecord?.rejectReason" show-icon :closable="false" style="margin-bottom: 20px;" />
        <kyc-form-component :kyc-method="kycMethodConfig" @submitted="fetchKYC" />
      </div>

      <!-- 未认证 -->
      <div v-else>
        <el-alert type="info" :title="t('user.kyc.statusNone')" show-icon :closable="false" style="margin-bottom: 20px;" />
        <kyc-form-component :kyc-method="kycMethodConfig" @submitted="fetchKYC" />
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getUserKYC, queryAlipayKYCResult } from '@/api/features'
import { useFeatureStore } from '@/pinia/modules/feature'

const { t } = useI18n()
const featureStore = useFeatureStore()

const kycStatus = ref('none')
const kycRecord = ref(null)
const queryLoading = ref(false)

const kycMethodConfig = computed(() => featureStore.kycMethod || 'manual')

async function fetchKYC() {
  try {
    const res = await getUserKYC()
    if (res.code === 200) {
      if (res.data?.status) {
        kycStatus.value = res.data.status
        kycRecord.value = res.data
      } else {
        kycStatus.value = 'none'
      }
    }
  } catch {
    kycStatus.value = 'none'
  }
}

async function handleQueryAlipay() {
  queryLoading.value = true
  try {
    const res = await queryAlipayKYCResult()
    if (res.code === 200) {
      if (res.data?.passed) {
        ElMessage.success(t('user.kyc.alipayVerifySuccess'))
        await fetchKYC()
      } else {
        ElMessage.warning(t('user.kyc.alipayNotPassed'))
      }
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.msg || t('user.kyc.queryFailed'))
  } finally {
    queryLoading.value = false
  }
}

onMounted(() => fetchKYC())
</script>

<script>
import { defineComponent, ref as componentRef, h as vueH, resolveComponent as resolveVueComponent } from 'vue'
import { ElMessage as ElementMessage } from 'element-plus'
import { useI18n as useVueI18n } from 'vue-i18n'
import { submitUserKYC as submitUserKYCRequest, submitAlipayKYC as submitAlipayKYCRequest } from '@/api/features'

// 内联KYC表单组件
const KycFormComponent = defineComponent({
  name: 'KycFormComponent',
  props: {
    kycMethod: { type: String, default: 'manual' }
  },
  emits: ['submitted'],
  setup(props, { emit }) {
    const { t } = useVueI18n()
    const submitting = componentRef(false)
    const selectedMethod = componentRef(props.kycMethod === 'both' ? 'manual' : props.kycMethod)
    const form = componentRef({ realName: '', idNumber: '' })

    async function handleSubmit() {
      if (!form.value.realName || !form.value.idNumber) {
        ElementMessage.warning(t('user.kyc.fillAllFields'))
        return
      }
      submitting.value = true
      try {
        if (selectedMethod.value === 'alipay') {
          const res = await submitAlipayKYCRequest(form.value)
          if (res.code === 200) {
            ElementMessage.success(t('user.kyc.alipayRedirectTip'))
            if (res.data?.certifyUrl) {
              window.open(res.data.certifyUrl, '_blank')
            }
            emit('submitted')
          }
        } else {
          const res = await submitUserKYCRequest(form.value)
          if (res.code === 200) {
            ElementMessage.success(t('user.kyc.submitSuccess'))
            emit('submitted')
          }
        }
      } finally {
        submitting.value = false
      }
    }

    return () => {
      const ElForm = resolveVueComponent('el-form')
      const ElFormItem = resolveVueComponent('el-form-item')
      const ElInput = resolveVueComponent('el-input')
      const ElButton = resolveVueComponent('el-button')
      const ElRadioGroup = resolveVueComponent('el-radio-group')
      const ElRadio = resolveVueComponent('el-radio')
      const ElAlert = resolveVueComponent('el-alert')

      const children = []

      // Method selection if both methods enabled
      if (props.kycMethod === 'both') {
        children.push(
          vueH(ElFormItem, { label: t('user.kyc.verifyMethod') }, () => [
            vueH(ElRadioGroup, {
              modelValue: selectedMethod.value,
              'onUpdate:modelValue': v => { selectedMethod.value = v }
            }, () => [
              vueH(ElRadio, { value: 'manual' }, () => t('user.kyc.methodManual')),
              vueH(ElRadio, { value: 'alipay' }, () => t('user.kyc.methodAlipay'))
            ])
          ])
        )
      }

      if (selectedMethod.value === 'alipay') {
        children.push(
          vueH(ElAlert, {
            type: 'info',
            title: t('user.kyc.alipayTip'),
            showIcon: true,
            closable: false,
            style: 'margin-bottom: 16px;'
          })
        )
      }

      children.push(
        vueH(ElFormItem, { label: t('user.kyc.realName'), prop: 'realName' }, () => [
          vueH(ElInput, {
            modelValue: form.value.realName,
            'onUpdate:modelValue': v => { form.value.realName = v },
            placeholder: t('user.kyc.realNamePlaceholder')
          })
        ]),
        vueH(ElFormItem, { label: t('user.kyc.idNumber'), prop: 'idNumber' }, () => [
          vueH(ElInput, {
            modelValue: form.value.idNumber,
            'onUpdate:modelValue': v => { form.value.idNumber = v },
            placeholder: t('user.kyc.idNumberPlaceholder')
          })
        ]),
        vueH(ElFormItem, {}, () => [
          vueH(ElButton, {
            type: 'primary',
            loading: submitting.value,
            onClick: handleSubmit
          }, () => selectedMethod.value === 'alipay'
            ? t('user.kyc.startAlipayVerify')
            : t('user.kyc.submit'))
        ])
      )

      return vueH(ElForm, { model: form.value, labelWidth: '120px' }, () => children)
    }
  }
})

export default { components: { KycFormComponent } }
</script>

<style scoped>
.kyc-container { padding: 20px; }
</style>

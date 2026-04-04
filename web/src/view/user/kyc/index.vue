<template>
  <div class="kyc-container">
    <el-card>
      <template #header>
        <span>{{ t('user.kyc.title') }}</span>
      </template>

      <!-- 已通过 -->
      <el-result v-if="kycStatus === 'approved'" icon="success" :title="t('user.kyc.statusApproved')" :sub-title="t('user.kyc.alreadyVerified')" />

      <!-- 审核中 -->
      <el-result v-else-if="kycStatus === 'pending'" icon="warning" :title="t('user.kyc.statusPending')" :sub-title="t('user.kyc.pendingReview')" />

      <!-- 已拒绝 -->
      <div v-else-if="kycStatus === 'rejected'">
        <el-alert type="error" :title="t('user.kyc.statusRejected')" :description="kycRecord?.rejectReason" show-icon :closable="false" style="margin-bottom: 20px;" />
        <kyc-form @submitted="fetchKYC" />
      </div>

      <!-- 未认证 -->
      <div v-else>
        <el-alert type="info" :title="t('user.kyc.statusNone')" show-icon :closable="false" style="margin-bottom: 20px;" />
        <kyc-form @submitted="fetchKYC" />
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, defineComponent, h } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getUserKYC, submitUserKYC } from '@/api/features'

const { t } = useI18n()

const kycStatus = ref('none')
const kycRecord = ref(null)

async function fetchKYC() {
  try {
    const res = await getUserKYC()
    if (res.code === 0 || res.code === 200) {
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

onMounted(() => fetchKYC())

// 内联KYC表单组件
const kycForm = defineComponent({
  emits: ['submitted'],
  setup(_, { emit }) {
    const formRef = ref(null)
    const submitting = ref(false)
    const form = ref({ realName: '', idNumber: '' })

    async function handleSubmit() {
      submitting.value = true
      try {
        const res = await submitUserKYC(form.value)
        if (res.code === 0 || res.code === 200) {
          ElMessage.success(t('user.kyc.submitSuccess'))
          emit('submitted')
        }
      } finally {
        submitting.value = false
      }
    }

    return () => h('div', [
      h('el-form', { ref: formRef, model: form.value, labelWidth: '120px' }, [
        h('el-form-item', { label: t('user.kyc.realName'), prop: 'realName' }, [
          h('el-input', { modelValue: form.value.realName, 'onUpdate:modelValue': v => form.value.realName = v })
        ]),
        h('el-form-item', { label: t('user.kyc.idNumber'), prop: 'idNumber' }, [
          h('el-input', { modelValue: form.value.idNumber, 'onUpdate:modelValue': v => form.value.idNumber = v })
        ]),
        h('el-form-item', {}, [
          h('el-button', { type: 'primary', loading: submitting.value, onClick: handleSubmit }, t('user.kyc.submit'))
        ])
      ])
    ])
  }
})
</script>

<style scoped>
.kyc-container { padding: 20px; }
</style>

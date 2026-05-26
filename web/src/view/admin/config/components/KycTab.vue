<template>
  <el-form
    v-loading="loading"
    :model="config"
    label-width="160px"
    class="config-form"
  >
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.kycMethod')">
          <el-select
            v-model="config.kyc.method"
            style="width: 100%"
          >
            <el-option
              value="manual"
              :label="$t('admin.config.kycMethodManual')"
            />
            <el-option
              value="alipay"
              :label="$t('admin.config.kycMethodAlipay')"
            />
            <el-option
              value="both"
              :label="$t('admin.config.kycMethodBoth')"
            />
          </el-select>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.kycRequireRealName')">
          <el-switch v-model="config.kyc.requireRealName" />
          <div class="form-item-hint">
            {{ $t('admin.config.kycRequireRealNameHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <el-divider>{{ $t('admin.config.kycRestrictions') }}</el-divider>

    <el-row :gutter="20">
      <el-col :span="8">
        <el-form-item :label="$t('admin.config.kycRestrictCreateInstance')">
          <el-switch v-model="config.kyc.restrictCreateInstance" />
        </el-form-item>
      </el-col>
      <el-col :span="8">
        <el-form-item :label="$t('admin.config.kycRestrictRedeemCode')">
          <el-switch v-model="config.kyc.restrictRedeemCode" />
        </el-form-item>
      </el-col>
      <el-col :span="8">
        <el-form-item :label="$t('admin.config.kycRestrictDomainBind')">
          <el-switch v-model="config.kyc.restrictDomainBind" />
        </el-form-item>
      </el-col>
    </el-row>

    <template v-if="config.kyc.method === 'alipay' || config.kyc.method === 'both'">
      <el-divider>{{ $t('admin.config.kycAlipayConfig') }}</el-divider>

      <el-row :gutter="20">
        <el-col :span="12">
          <el-form-item :label="$t('admin.config.kycAlipayAppId')">
            <el-input
              v-model="config.kyc.alipayAppId"
              :placeholder="$t('admin.config.kycAlipayAppId')"
            />
          </el-form-item>
        </el-col>
      </el-row>
      <el-row :gutter="20">
        <el-col :span="12">
          <el-form-item :label="$t('admin.config.kycAlipayPrivateKey')">
            <el-input
              v-model="config.kyc.alipayPrivateKey"
              type="password"
              show-password
              :placeholder="$t('admin.config.kycAlipayPrivateKey')"
            />
          </el-form-item>
        </el-col>
        <el-col :span="12">
          <el-form-item :label="$t('admin.config.kycAlipayPublicKey')">
            <el-input
              v-model="config.kyc.alipayPublicKey"
              type="password"
              show-password
              :placeholder="$t('admin.config.kycAlipayPublicKey')"
            />
          </el-form-item>
        </el-col>
      </el-row>
    </template>
  </el-form>
</template>

<script setup>
defineProps({
  config: { type: Object, required: true },
  loading: { type: Boolean, default: false }
})
</script>

<style scoped>
.config-form {
  max-height: 600px;
  overflow-y: auto;
}
.form-item-hint {
  font-size: 12px;
  color: var(--text-color-tertiary);
  margin-top: 4px;
  line-height: 1.4;
}
</style>

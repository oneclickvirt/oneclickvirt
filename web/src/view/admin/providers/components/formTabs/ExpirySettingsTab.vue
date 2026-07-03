<template>
  <div class="server-form">
    <el-form
      :model="modelValue"
      label-width="120px"
    >
      <el-divider content-position="left">
        <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.providerExpirySettings') }}</span>
      </el-divider>

      <el-form-item
        :label="$t('admin.providers.expiresAt')"
        prop="expiresAt"
      >
        <el-date-picker
          v-model="modelValue.expiresAt"
          type="datetime"
          :placeholder="$t('admin.providers.expiresAtPlaceholder')"
          format="YYYY-MM-DD HH:mm:ss"
          value-format="YYYY-MM-DD HH:mm:ss"
          :disabled-date="(time) => time.getTime() < Date.now() - 8.64e7"
        />
      </el-form-item>
      <div
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.expiresAtTip') }}
        </el-text>
      </div>

      <el-divider content-position="left">
        <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.trafficResetSettings') }}</span>
      </el-divider>

      <el-form-item
        :label="$t('admin.providers.trafficResetDay')"
        prop="trafficResetDay"
      >
        <el-select
          v-model="modelValue.trafficResetDay"
          :placeholder="$t('admin.providers.trafficResetDayPlaceholder')"
          clearable
          filterable
          style="width: 240px"
        >
          <el-option
            v-for="day in trafficResetDayOptions"
            :key="day"
            :label="$t('admin.providers.trafficResetDayOption', { day })"
            :value="day"
          />
        </el-select>
      </el-form-item>
      <div
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.trafficResetDayTip') }}
        </el-text>
      </div>

      <el-divider content-position="left">
        <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.instanceExpirySettings') }}</span>
      </el-divider>

      <el-form-item
        :label="$t('admin.providers.instanceExpiryAction')"
        prop="instanceExpiryAction"
      >
        <el-select
          v-model="modelValue.instanceExpiryAction"
          :placeholder="$t('admin.providers.instanceExpiryActionPlaceholder')"
          style="width: 260px"
        >
          <el-option
            :label="$t('admin.providers.expiryActionDelete')"
            value="delete"
          />
          <el-option
            :label="$t('admin.providers.expiryActionFreeze')"
            value="freeze"
          />
          <el-option
            :label="$t('admin.providers.expiryActionStop')"
            value="stop"
          />
          <el-option
            :label="$t('admin.providers.expiryActionExtend')"
            value="extend"
          />
        </el-select>
      </el-form-item>
      <div
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.instanceExpiryActionTip') }}
        </el-text>
      </div>

      <el-form-item
        v-if="modelValue.instanceExpiryAction === 'extend'"
        :label="$t('admin.providers.instanceExpiryExtendDays')"
        prop="instanceExpiryExtendDays"
      >
        <el-input-number
          v-model="modelValue.instanceExpiryExtendDays"
          :min="1"
          :max="365"
          :step="1"
          :controls="false"
          style="width: 200px"
        />
        <span style="margin-left: 10px; color: #666;">{{ $t('common.days') }}</span>
      </el-form-item>
    </el-form>

    <template v-if="isEditing && providerId">
      <el-divider content-position="left">
        <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.checkinConfig') }}</span>
      </el-divider>

      <CheckinConfigTab
        :provider-id="providerId"
        embedded
      />
    </template>
  </div>
</template>

<script setup>
import CheckinConfigTab from './CheckinConfigTab.vue'

defineProps({
  modelValue: {
    type: Object,
    required: true
  },
  providerId: {
    type: [Number, String],
    default: null
  },
  isEditing: {
    type: Boolean,
    default: false
  }
})

const trafficResetDayOptions = Array.from({ length: 31 }, (_, index) => index + 1)
</script>

<style scoped>
.server-form {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

.form-tip {
  margin-top: 5px;
}
</style>

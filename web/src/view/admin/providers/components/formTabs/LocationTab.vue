<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <el-form-item
      :label="$t('admin.providers.region')"
      prop="region"
    >
      <el-input
        v-model="modelValue.region"
        :placeholder="$t('admin.providers.regionPlaceholder')"
      />
    </el-form-item>
    <el-form-item
      :label="$t('admin.providers.country')"
      prop="country"
    >
      <el-select 
        v-model="modelValue.country" 
        :placeholder="$t('admin.providers.countryPlaceholder')"
        filterable
      >
        <el-option-group
          v-for="(regionCountries, region) in groupedCountries"
          :key="region"
          :label="region"
        >
          <el-option 
            v-for="country in regionCountries" 
            :key="country.code" 
            :label="`${country.flag} ${country.name}`" 
            :value="country.name"
          />
        </el-option-group>
      </el-select>
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.countryTip') }}
        </el-text>
      </div>
    </el-form-item>
    <el-form-item
      :label="$t('admin.providers.city')"
      prop="city"
    >
      <el-input
        v-model="modelValue.city"
        :placeholder="$t('admin.providers.cityPlaceholder')"
        clearable
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.cityTip') }}
        </el-text>
      </div>
    </el-form-item>
  </el-form>
</template>

<script setup>
defineProps({
  modelValue: {
    type: Object,
    required: true
  },
  groupedCountries: {
    type: Object,
    required: true
  }
})
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

<template>
  <div class="menu-item">
    <el-icon
      v-if="icon"
      class="menu-icon"
    >
      <component :is="icon" />
    </el-icon>
    <span
      v-if="!isCollapse"
      class="menu-title"
    >{{ $t(title) }}</span>
  </div>
</template>

<script setup>
defineProps({
  icon: {
    type: String,
    default: ''
  },
  title: {
    type: String,
    default: ''
  },
  isCollapse: {
    type: Boolean,
    default: false
  }
})
</script>

<style scoped>
.menu-item {
  display: flex;
  align-items: center;
}

.menu-icon {
  margin-right: 8px;
  font-size: 16px;
}

.menu-title {
  font-size: 14px;
}
</style>
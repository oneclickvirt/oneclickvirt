<template>
  <footer class="app-footer">
    <div class="app-footer-inner">
      <span class="footer-copyright">&copy; 2026 {{ siteStore.displaySiteName }}. {{ t('home.footer.allRightsReserved') }}</span>
      <span class="footer-divider" />
      <a
        href="https://github.com/oneclickvirt"
        target="_blank"
        rel="noopener noreferrer"
        class="footer-link"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="currentColor"
          class="footer-github-icon"
        >
          <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
        </svg>
        {{ t('home.footer.openSourceProject') }}
      </a>
      <span
        v-if="serverVersion"
        class="footer-divider"
      />
      <span
        v-if="serverVersion"
        class="footer-version"
        :title="`${t('home.footer.serverVersion')} ${serverVersion}`"
      >
        <span>{{ t('home.footer.serverVersion') }}</span>
        <span class="footer-version-value">{{ serverVersion }}</span>
      </span>
      <a
        v-if="updateAvailable && latestVersion"
        :href="releaseUrl || 'https://github.com/oneclickvirt/oneclickvirt/releases'"
        target="_blank"
        rel="noopener noreferrer"
        class="footer-update-link"
        :title="`${t('home.footer.latestVersion')} ${latestVersion}`"
      >
        <span>{{ t('home.footer.latestVersion') }}</span>
        <span class="footer-version-value">{{ latestVersion }}</span>
      </a>
      <span
        v-if="versionFetchFailed"
        class="footer-divider"
      />
      <span
        v-if="versionFetchFailed"
        class="footer-version-error"
      >
        {{ t('home.footer.versionFetchFailed') }}
      </span>
    </div>
  </footer>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSiteStore } from '@/pinia/modules/site'
import { getServerVersion } from '@/api/public'

const { t } = useI18n()
const siteStore = useSiteStore()
const serverVersion = ref('')
const latestVersion = ref('')
const releaseUrl = ref('')
const updateAvailable = ref(false)
const versionFetchFailed = ref(false)

onMounted(async () => {
  try {
    const res = await getServerVersion()
    if (res && (res.code === 200) && res.data?.server_version) {
      serverVersion.value = res.data.server_version
      latestVersion.value = res.data.latest_version || ''
      releaseUrl.value = res.data.release_url || ''
      updateAvailable.value = Boolean(res.data.update_available)
      versionFetchFailed.value = res.data.version_check_status === 'failed'
    } else {
      versionFetchFailed.value = true
    }
  } catch {
    versionFetchFailed.value = true
  }
})
</script>

<style lang="scss" scoped>
.app-footer {
  width: 100%;
  background-color: var(--bg-color-secondary);
  border-top: 1px solid var(--border-color);
  padding: 9px 0;
  margin-top: auto;
  flex-shrink: 0;
}

.app-footer-inner {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  flex-wrap: wrap;
  min-width: 0;
  padding: 0 var(--spacing-lg);
}

@media (max-width: 768px) {
  .app-footer {
    padding: 8px 0 calc(8px + env(safe-area-inset-bottom));
  }

  .app-footer-inner {
    gap: 8px;
    padding: 0 12px;
  }

  .footer-copyright,
  .footer-link {
    font-size: 12px;
  }
}

.footer-copyright {
  font-size: 13px;
  color: var(--text-color-secondary);
}

.footer-divider {
  display: inline-block;
  width: 1px;
  height: 14px;
  background-color: var(--border-color);
  vertical-align: middle;
}

.footer-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  font-size: 13px;
  color: var(--primary-color);
  text-decoration: none;
  transition: var(--transition-all);

  &:hover {
    color: var(--primary-color-dark);
  }
}

.footer-github-icon {
  flex-shrink: 0;
}

.footer-version {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  max-width: 100%;
  min-width: 0;
  font-size: 12px;
  color: var(--text-color-placeholder);
  font-family: monospace;
  line-height: 1.5;
  overflow-wrap: anywhere;
  white-space: normal;
}

.footer-update-link {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  max-width: 100%;
  min-width: 0;
  font-size: 12px;
  color: var(--el-color-success);
  text-decoration: none;
  font-family: monospace;
  line-height: 1.5;
  overflow-wrap: anywhere;
  white-space: normal;

  &:hover {
    text-decoration: underline;
  }
}

.footer-version-value {
  min-width: 0;
  word-break: break-all;
  overflow-wrap: anywhere;
}

.footer-version-error {
  font-size: 12px;
  color: var(--el-color-warning);
}
</style>

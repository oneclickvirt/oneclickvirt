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
    </div>
  </footer>
</template>

<script setup>
import { useI18n } from 'vue-i18n'
import { useSiteStore } from '@/pinia/modules/site'

const { t } = useI18n()
const siteStore = useSiteStore()
</script>

<style lang="scss" scoped>
.app-footer {
  width: 100%;
  background-color: var(--bg-color-secondary);
  border-top: 1px solid var(--border-color);
  padding: 12px 0;
  margin-top: auto;
  flex-shrink: 0;
}

.app-footer-inner {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 0 var(--spacing-lg);
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
</style>

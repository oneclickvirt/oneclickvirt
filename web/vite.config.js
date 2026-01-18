import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  return {
    base: mode === 'production' ? '/' : '/',
    plugins: [vue()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url))
      }
    },
    server: {
      port: parseInt(env.VITE_CLI_PORT) || 8080,
      open: true,
      proxy: {
        [env.VITE_BASE_API || '/api']: {
          target: `${env.VITE_BASE_PATH}:${env.VITE_SERVER_PORT}` || 'http://0.0.0.0:8888',
          changeOrigin: true,
          // 可选：调试日志
          configure: (proxy, options) => {
            proxy.on('proxyReq', (proxyReq, req, res) => {
              // console.log(`代理请求: ${req.method} ${req.url} -> ${options.target}${proxyReq.path}`)
            })
          }
        }
      }
    },
    css: {
      preprocessorOptions: {
        scss: {
          additionalData: `@use '@/style/theme.scss' as *;`
        }
      }
    },
    build: {
      outDir: 'dist',
      assetsDir: 'assets',
      sourcemap: false,
      minify: 'terser',
      rollupOptions: {
        output: {
          chunkFileNames: 'js/[name]-[hash].js',
          entryFileNames: 'js/[name]-[hash].js',
          assetFileNames: 'assets/[name]-[hash].[ext]'
        }
      },
      terserOptions: {
        compress: {
          // 暂时保留 console.log 用于调试 Docker 环境中的注册问题
          drop_console: false,
          drop_debugger: mode === 'production'
        }
      }
    }
  }
})
import { fileURLToPath, URL } from 'node:url'
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  return {
    plugins: [vue()],
    resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
    server: {
      proxy: {
        '/api': {
          target: env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:8000',
          // Keep the browser Host: the Origin/CSRF check compares it with the
          // Origin header, and a rewritten Host would look like a cross-site
          // request. A production reverse proxy must preserve Host too.
          changeOrigin: false,
        },
      },
    },
  }
})

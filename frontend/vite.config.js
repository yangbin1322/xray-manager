import { defineConfig } from 'vite'

export default defineConfig({
  build: {
    rollupOptions: {
      external: ['@wailsio/runtime'],
      output: {
        // 保留原始的 import 语句，由 importmap 处理
        format: 'es'
      }
    }
  }
})

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    rollupOptions: {
      external: ['@wailsio/runtime'],
      output: {
        format: 'es'
      }
    }
  }
})

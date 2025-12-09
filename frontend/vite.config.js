import { defineConfig } from 'vite'

export default defineConfig({
  build: {
    rollupOptions: {
      external: ['@wailsio/runtime'],
      output: {
        paths: {
          '@wailsio/runtime': '/wails/runtime.js'
        }
      }
    }
  }
})

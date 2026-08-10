import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    // The graph chunk is deliberately large: vis-network is ~515 kB and is
    // reached only through a dynamic import, so it never touches the entry
    // bundle or the login route. Raising the threshold above it keeps the
    // warning meaningful — it should fire if something big lands in a chunk
    // that *is* on the critical path, not on the one chunk we chose to isolate.
    chunkSizeWarningLimit: 600,
  },
})

import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    // In dev the Go backend runs separately (make dev); proxy API + WS to it.
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', ws: true },
    },
  },
});

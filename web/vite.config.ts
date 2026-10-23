import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    // In dev the Go backend runs separately (make dev); proxy API + WS to it.
    // MINOS_API points the dev UI at another instance — e.g. a Pi with a real
    // multi-million-row log, where slow-path UI behaviour actually shows.
    proxy: {
      '/api': { target: process.env.MINOS_API ?? 'http://127.0.0.1:8080', ws: true },
    },
  },
});

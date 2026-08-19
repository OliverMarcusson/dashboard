import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The build lands directly in the Go package that embeds it, so `go build`
// always ships whatever `npm run build` last produced.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:13000',
    },
  },
});

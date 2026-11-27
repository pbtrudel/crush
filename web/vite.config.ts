import { defineConfig } from 'vitest/config';
import preact from '@preact/preset-vite';
import type { ProxyOptions } from 'vite';

// CRUSH_WEB_DEV_PORT is the port of a running gateway; `task web:dev` prints it.
const target = `http://127.0.0.1:${process.env.CRUSH_WEB_DEV_PORT ?? '7777'}`;

// The gateway rejects foreign origins. The dev proxy forwards same-origin
// requests, so drop the Vite origin header on the way through.
const stripOrigin: ProxyOptions['configure'] = (proxy) => {
  proxy.on('proxyReq', (req) => req.removeHeader('origin'));
  proxy.on('proxyReqWs', (req) => req.removeHeader('origin'));
};

export default defineConfig({
  plugins: [preact()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // CSP forbids data: fonts and inline scripts.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
    sourcemap: false,
    chunkSizeWarningLimit: 700,
  },
  server: {
    proxy: { '/api': { target, changeOrigin: true, ws: true, configure: stripOrigin } },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
});

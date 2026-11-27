import react from '@vitejs/plugin-react'
import type { ProxyOptions } from 'vite'
import { defineConfig } from 'vitest/config'

// CRUSH_WEB_DEV_PORT is the port of a running gateway; webfake prints its
// launch URL on stdout, and that URL carries the port.
const target = `http://127.0.0.1:${process.env.CRUSH_WEB_DEV_PORT ?? '7777'}`

// The gateway rejects foreign origins. The dev proxy forwards same-origin
// requests, so drop the Vite origin header on the way through.
const stripOrigin: ProxyOptions['configure'] = (proxy) => {
  proxy.on('proxyReq', (req) => req.removeHeader('origin'))
  proxy.on('proxyReqWs', (req) => req.removeHeader('origin'))
}

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: { '/api': { target, changeOrigin: true, ws: true, configure: stripOrigin } },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    css: true,
  },
})

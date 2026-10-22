import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
const standalone = process.env.STANDALONE_HTML === '1';
export default defineConfig({
  plugins: [vue()],
  base: './',
  esbuild: { legalComments: 'eof' },
  build: {
    copyPublicDir: !standalone,
    outDir: standalone ? 'dist-offline' : 'dist',
    chunkSizeWarningLimit: 1300,
    rollupOptions: {
      output: {
        inlineDynamicImports: standalone,
        manualChunks: standalone
          ? undefined
          : {
              three: ['three', 'camera-controls', 'postprocessing'],
              charts: ['echarts'],
            },
      },
    },
  },
});

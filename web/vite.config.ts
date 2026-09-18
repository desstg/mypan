import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// 构建产物写入 Go 内嵌目录，开发态将 /api 代理到后端。
export default defineConfig({
  plugins: [
    vue({
      template: {
        compilerOptions: {
          isCustomElement: (tag) => tag.startsWith("media-"),
        },
      },
    }),
  ],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    // 监听所有网卡，便于用局域网 IP（如 192.168.31.14:5173）在手机/其他设备上看效果。
    // 仅绑定 localhost 时 Vite 默认只监听 IPv6 [::1]，IPv4 访问会失败。
    host: true,
    proxy: {
      "/api": {
        target: process.env.LITEPAN_API_PROXY || "http://127.0.0.1:5211",
        // 不要开 changeOrigin：它会把 Host 改写成代理目标（127.0.0.1:5211），
        // 而后端对写接口会校验「请求来源 == 本站地址」（pkg/security/origin.go，防 CSRF）。
        // 于是用局域网地址打开开发页时，Origin 是 192.168.x.x:5173、Host 却是 127.0.0.1:5211，
        // 校验失败 → 所有 POST/PUT 报「请求来源不受信任」。上面 host: true 的局域网访问
        // 就被这一行废掉了。保持原始 Host 后两者自然一致，任何地址都能用。
        changeOrigin: false,
        // 超过 vite 默认超时的接口要显式放宽：115 扫码的状态查询是**长轮询**，
        // 一次请求会挂满约 30 秒（见 drivers/115_Open/qrlogin.go 顶部）。
        // 默认值下代理会提前掐断连接，前端拿到一个空响应 —— 表现是「扫了码
        // 但弹窗一直停在等待」，而后端其实好好地拿到了结果。
        timeout: 120_000,
        proxyTimeout: 120_000,
      },
    },
  },
  build: {
    outDir: "../internal/api/web",
    emptyOutDir: true,
    // 解码器按需分包，阈值略高于当前最大独立产物。
    chunkSizeWarningLimit: 3200,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules/three')) {
            return 'three-vendor';
          }
          if (id.includes('node_modules/vue') || id.includes('node_modules/pinia') || id.includes('node_modules/vue-router')) {
            return 'vue-vendor';
          }
        }
      }
    }
  },
});

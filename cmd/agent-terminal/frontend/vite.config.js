import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

// 把 vendored 的 lib/ 目录映射成短别名，免去 ../../../ 数路径的痛点。
// - '@vue'  → frontend/lib/vue.esm-browser.prod.js（Vue 3 浏览器构建）
// - '@lib'  → frontend/lib/                          （所有 vendored 库的根）
// 编辑器跳转/补全请同时见 ./jsconfig.json 的 paths 配置。
const VUE_LIB = fileURLToPath(new URL("./lib/vue.esm-browser.prod.js", import.meta.url));
const LIB_DIR = fileURLToPath(new URL("./lib", import.meta.url));

// https://vitejs.dev/config/
export default defineConfig({
    // Vite 持久化 transform 缓存：避免每次 build 重新处理 2600+ 模块
    cacheDir: ".vite-cache",

    resolve: {
        alias: {
            "@vue": VUE_LIB,
            "@lib": LIB_DIR,
        },
    },

    // Wails 前端: 输出到 dist/, Go embed 打包
    build: {
        outDir: "dist",
        emptyOutDir: true,
        rollupOptions: {
            external: ["/wails/runtime.js"],
            output: {
                manualChunks: {
                    "vendor-echarts": ["echarts/core", "echarts/charts", "echarts/components", "echarts/renderers"],
                    "vendor-md": ["markdown-it", "@vscode/markdown-it-katex", "katex"],
                    "vendor-hljs": ["highlight.js/lib/core"],
                    "vendor-mermaid": ["mermaid"],
                    "vendor-pretext": ["@chenglou/pretext"],
                },
            },
        },
    },

    server: {
        port: 5173,
        strictPort: true,
        proxy: {
            "/wails/ws": {
                target: "ws://127.0.0.1:4511",
                ws: true,
            },
        },
    },
});


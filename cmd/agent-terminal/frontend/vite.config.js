import { defineConfig } from "vite";

// https://vitejs.dev/config/
export default defineConfig({
    // Vite 持久化 transform 缓存：避免每次 build 重新处理 2600+ 模块
    cacheDir: ".vite-cache",

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
    },
});


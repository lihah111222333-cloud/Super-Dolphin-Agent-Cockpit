import { defineConfig } from "vite";

// https://vitejs.dev/config/
export default defineConfig({
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


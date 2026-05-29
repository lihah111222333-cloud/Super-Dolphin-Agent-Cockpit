import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";

const VUE_SHIM = fileURLToPath(new URL("./lib/vue-react-shim.js", import.meta.url));
const LIB_DIR = fileURLToPath(new URL("./lib", import.meta.url));
const SRC_DIR = fileURLToPath(new URL("./src", import.meta.url));

// https://vitejs.dev/config/
export default defineConfig({
    cacheDir: ".vite-cache",
    plugins: [react()],
    resolve: {
        alias: [
            { find: /^\/vue-app\/(.*)/, replacement: `${SRC_DIR}/$1` },
            {
                find: /^(.*)\/(UnifiedChatPage|ActivityPanel|ChatTimeline|ComposerBar|ComposerForkDraftCard|ContextUsageBanner|DiffPanel|JsonRenderWidgets|JsonRenderer|McBadge|McButton|McCard|PathChoiceModal|ProjectModal|ProjectSelect|SidebarNav|json-render-markdown-action-key|ChatToolbar|CmdCardGrid|CmdOverviewPanel|ThreadRailSidePanel|WorkspaceChatPanel|AttachmentPreview|ToolTickerBar)\.(js|ts)$/,
                replacement: "$1/$2.jsx"
            },
            { find: /.*vue\.esm-browser\.prod\.js$/, replacement: VUE_SHIM },
            { find: "@vue", replacement: VUE_SHIM },
            { find: "@lib", replacement: LIB_DIR },
            { find: "@", replacement: SRC_DIR },
        ],
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

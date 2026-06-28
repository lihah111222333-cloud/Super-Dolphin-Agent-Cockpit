/**
 * JsonRenderer — 递归渲染 json-render spec 的 Vue 组件。
 * 接收一个 spec 对象 (含 type 字段)，根据 WIDGET_REGISTRY 查找对应组件进行渲染。
 */
import { h, defineComponent, inject, provide } from '../../lib/vue.esm-browser.prod.js';
import { WIDGET_REGISTRY } from './JsonRenderWidgets.js';
import { JSON_RENDER_MARKDOWN_ACTION_KEY } from './json-render-markdown-action-key.js';

export const JsonRenderer = defineComponent({
    name: 'JsonRenderer',
    props: {
        spec: { type: Object, required: true },
        markdownActionHandlers: { type: Object, default: null },
    },
    emits: ['file-ref-click', 'citation-click'],
    setup(props, { emit }) {
        const inheritedMarkdownActionHandlers = inject(JSON_RENDER_MARKDOWN_ACTION_KEY, null);
        const markdownActionHandlers = props.markdownActionHandlers || inheritedMarkdownActionHandlers || null;
        provide(JSON_RENDER_MARKDOWN_ACTION_KEY, markdownActionHandlers);

        return () => {
            const spec = props.spec;
            if (!spec || typeof spec !== 'object') {
                return h('span', { class: 'jr-empty' }, '(empty)');
            }

            const typeName = (spec.type || '').toString().trim();
            const entry = WIDGET_REGISTRY[typeName];

            if (!entry) {
                return h('div', { class: 'jr-unknown' }, [
                    h('span', { class: 'jr-unknown-type' }, `Unknown: ${typeName || '(no type)'}`),
                ]);
            }

            return h(entry.component, {
                spec,
                onFileRefClick: (payload) => emit('file-ref-click', payload),
                onCitationClick: (payload) => emit('citation-click', payload),
            });
        };
    },
});

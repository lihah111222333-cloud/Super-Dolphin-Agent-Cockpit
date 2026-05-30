/**
 * JsonRenderWidgets — json-render 组件注册表。
 * 每个 widget 定义了一个 Vue 组件，用于渲染特定 type 的 spec。
 */
import { h, defineComponent, ref, onMounted, onBeforeUnmount, watch, nextTick, inject } from '../../lib/vue.esm-browser.prod.js';
import { renderAssistantMarkdown } from '../utils/assistant-markdown.js';
import { resolveRenderedMarkdownAction } from '../utils/assistant-markdown-click.js';
import echarts from '../lib/echarts-custom.js';
import { JSON_RENDER_MARKDOWN_ACTION_KEY } from './json-render-markdown-action-key.js';

// ──── 递归渲染子 spec 的辅助函数 ────
function renderChildren(children, JsonRenderer) {
    if (!Array.isArray(children)) return [];
    return children.map((child, i) => {
        if (typeof child === 'string') return child;
        if (child && typeof child === 'object' && child.type) {
            return h(JsonRenderer, { key: i, spec: child });
        }
        return null;
    });
}

// 懒加载 JsonRenderer 避免循环依赖
let _JsonRenderer = null;
function getRenderer() {
    if (!_JsonRenderer) {
        _JsonRenderer = defineComponent({
            name: 'JrChildRenderer',
            props: { spec: Object },
            setup(props, { attrs }) {
                return () => {
                    const entry = WIDGET_REGISTRY[(props.spec?.type || '')];
                    if (!entry) return h('span', { class: 'jr-unknown' }, `Unknown: ${props.spec?.type}`);
                    return h(entry.component, { spec: props.spec, ...attrs });
                };
            },
        });
    }
    return _JsonRenderer;
}

// ──── Widget 组件定义 ────

const JrCard = defineComponent({
    name: 'JrCard',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const header = (s.title || s.description)
                ? h('div', { class: 'jr-card-header' }, [
                    s.title ? h('h3', { class: 'jr-card-title' }, s.title) : null,
                    s.description ? h('p', { class: 'jr-card-desc' }, s.description) : null,
                ])
                : null;
            const body = h('div', { class: 'jr-card-body' },
                renderChildren(s.children || [], getRenderer()));
            return h('div', { class: 'jr-root jr-card' }, [header, body]);
        };
    },
});

const JrMetric = defineComponent({
    name: 'JrMetric',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            return h('div', { class: 'jr-root jr-metric' }, [
                h('span', { class: 'jr-metric-label' }, s.label || ''),
                h('span', { class: 'jr-metric-value' }, String(s.value ?? '')),
            ]);
        };
    },
});

const JrStack = defineComponent({
    name: 'JrStack',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const dir = s.direction === 'row' ? 'jr-stack-row' : 'jr-stack-col';
            const gap = s.gap ? `${s.gap}px` : '8px';
            return h('div', {
                class: `jr-root jr-stack ${dir}`,
                style: { gap },
            }, renderChildren(s.children || [], getRenderer()));
        };
    },
});

const JrHeading = defineComponent({
    name: 'JrHeading',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const level = Math.min(Math.max(Number(s.level) || 2, 1), 4);
            const tag = `h${level}`;
            return h(tag, { class: 'jr-root jr-heading' }, s.text || '');
        };
    },
});

const JrTable = defineComponent({
    name: 'JrTable',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const cols = s.columns || [];
            const rows = s.rows || [];
            return h('div', { class: 'jr-root jr-table-wrap' }, [
                h('table', { class: 'jr-table' }, [
                    h('thead', [
                        h('tr', cols.map(c =>
                            h('th', { class: 'jr-table-th' }, typeof c === 'string' ? c : (c.label || c.key || ''))
                        )),
                    ]),
                    h('tbody', rows.map((row, ri) =>
                        h('tr', { key: ri }, cols.map((c, ci) => {
                            const key = typeof c === 'string' ? c : (c.key || c.label || '');
                            const val = Array.isArray(row) ? row[ci] : (row[key] ?? '');
                            return h('td', { class: 'jr-table-td' }, String(val));
                        }))
                    )),
                ]),
            ]);
        };
    },
});

const JrAlert = defineComponent({
    name: 'JrAlert',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const severity = s.severity || 'info';
            const icons = { info: 'ℹ️', warning: '⚠️', error: '❌', success: '✅' };
            return h('div', { class: `jr-root jr-alert jr-alert-${severity}` }, [
                h('span', { class: 'jr-alert-icon' }, icons[severity] || 'ℹ️'),
                h('div', { class: 'jr-alert-content' }, [
                    s.title ? h('strong', { class: 'jr-alert-title' }, s.title) : null,
                    h('span', { class: 'jr-alert-msg' }, s.message || ''),
                ]),
            ]);
        };
    },
});

const JrBadge = defineComponent({
    name: 'JrBadge',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const variant = s.variant || 'default';
            return h('span', { class: `jr-root jr-badge jr-badge-${variant}` }, s.text || '');
        };
    },
});

const JrCodeBlock = defineComponent({
    name: 'JrCodeBlock',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            return h('div', { class: 'jr-root', style: { position: 'relative' } }, [
                s.language ? h('span', { class: 'jr-codeblock-lang' }, s.language) : null,
                h('pre', { class: 'jr-codeblock' }, s.code || ''),
            ]);
        };
    },
});

const JrList = defineComponent({
    name: 'JrList',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const ordered = s.ordered === true;
            const tag = ordered ? 'ol' : 'ul';
            const items = (s.items || []).map((item, i) =>
                h('li', { key: i, class: 'jr-list-item' }, String(item))
            );
            return h(tag, { class: 'jr-root jr-list' }, items);
        };
    },
});

const JrProgress = defineComponent({
    name: 'JrProgress',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const pct = Math.min(100, Math.max(0, Number(s.value) || 0));
            return h('div', { class: 'jr-root jr-progress' }, [
                h('div', { class: 'jr-progress-header' }, [
                    s.label ? h('span', { class: 'jr-progress-label' }, s.label) : null,
                    h('span', { class: 'jr-progress-pct' }, `${pct}%`),
                ]),
                h('div', { class: 'jr-progress-track' }, [
                    h('div', { class: 'jr-progress-fill', style: { width: `${pct}%` } }),
                ]),
            ]);
        };
    },
});

const JrSeparator = defineComponent({
    name: 'JrSeparator',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            if (s.label) {
                return h('div', { class: 'jr-root jr-separator-labeled' }, [
                    h('hr', { class: 'jr-separator-line' }),
                    h('span', { class: 'jr-separator-text' }, s.label),
                    h('hr', { class: 'jr-separator-line' }),
                ]);
            }
            return h('hr', { class: 'jr-root jr-separator' });
        };
    },
});

const JrText = defineComponent({
    name: 'JrText',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            return h('p', { class: 'jr-root', style: { margin: 0, fontSize: '12px', lineHeight: '1.5' } }, s.text || '');
        };
    },
});

const JrMarkdown = defineComponent({
    name: 'JrMarkdown',
    props: { spec: Object },
    emits: ['file-ref-click', 'citation-click'],
    setup(props, { emit }) {
        const markdownActionHandlers = inject(JSON_RENDER_MARKDOWN_ACTION_KEY, null);
        function onMarkdownClick(event) {
            const action = resolveRenderedMarkdownAction(event);
            if (!action) return;
            if (typeof event?.preventDefault === 'function') event.preventDefault();
            if (typeof event?.stopPropagation === 'function') event.stopPropagation();
            if (action.type === 'file-ref') {
                markdownActionHandlers?.onFileRefClick?.(action.payload);
                emit('file-ref-click', action.payload);
                return;
            }
            if (action.type === 'citation') {
                markdownActionHandlers?.onCitationClick?.(action.payload);
                emit('citation-click', action.payload);
            }
        }
        return () => {
            const s = props.spec || {};
            const html = renderAssistantMarkdown((s.text || '').toString());
            return h('div', {
                class: 'jr-root jr-markdown chat-item-markdown agent-markdown-root',
                innerHTML: html,
                onClick: onMarkdownClick,
            });
        };
    },
});

// ──── Chart (ECharts) ────

const JrChart = defineComponent({
    name: 'JrChart',
    props: { spec: Object },
    setup(props) {
        const containerRef = ref(null);
        let chartInstance = null;
        let resizeObserver = null;
        const onWindowResize = () => {
            if (chartInstance) chartInstance.resize();
        };

        // 解析尺寸: 支持数字 (300) 和字符串 ("300px" / "50vh" / "100%")
        function parseSize(raw, fallback) {
            if (raw == null || raw === '') return fallback;
            const str = String(raw).trim();
            if (!str) return fallback;
            if (/^\d+(\.\d+)?$/.test(str)) return `${str}px`;
            return str;
        }

        function initChart() {
            if (!containerRef.value) return;
            if (chartInstance) { chartInstance.dispose(); chartInstance = null; }
            const theme = (typeof props.spec?.theme === 'string' && props.spec.theme.trim())
                ? props.spec.theme.trim()
                : 'dark';
            chartInstance = echarts.init(containerRef.value, theme, { renderer: 'canvas' });
            const option = props.spec?.option || props.spec || {};
            chartInstance.setOption(option, { notMerge: true });
            requestAnimationFrame(() => {
                if (chartInstance) chartInstance.resize();
            });
        }

        onMounted(() => {
            nextTick(initChart);
            if (typeof window !== 'undefined') {
                window.addEventListener('resize', onWindowResize);
            }
            // 监听容器可见性变化 (Tabs 切换时触发)
            if (containerRef.value && typeof ResizeObserver !== 'undefined') {
                resizeObserver = new ResizeObserver(() => {
                    if (!containerRef.value) return;
                    if (!chartInstance) {
                        initChart();
                    } else {
                        chartInstance.resize();
                    }
                });
                resizeObserver.observe(containerRef.value);
            }
        });

        watch(() => props.spec, () => {
            if (chartInstance) {
                const option = props.spec?.option || props.spec || {};
                chartInstance.setOption(option, { notMerge: true });
                nextTick(() => {
                    if (chartInstance) chartInstance.resize();
                });
            } else {
                nextTick(initChart);
            }
        }, { deep: true });

        onBeforeUnmount(() => {
            if (typeof window !== 'undefined') {
                window.removeEventListener('resize', onWindowResize);
            }
            if (resizeObserver) { resizeObserver.disconnect(); resizeObserver = null; }
            if (chartInstance) { chartInstance.dispose(); chartInstance = null; }
        });

        return () => h('div', {
            ref: containerRef,
            class: 'jr-root jr-chart',
            style: {
                width: parseSize(props.spec?.width, '100%'),
                height: parseSize(props.spec?.height, '300px'),
            },
        });
    },
});

// ──── Stat (指标+趋势) ────

const JrStat = defineComponent({
    name: 'JrStat',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const trend = s.trend || '';
            let trendClass = '';
            let trendIcon = '';
            if (trend === 'up') {
                trendClass = 'jr-stat-up';
                trendIcon = 'up';
            } else if (trend === 'down') {
                trendClass = 'jr-stat-down';
                trendIcon = 'down';
            }
            return h('div', { class: 'jr-root jr-stat' }, [
                h('span', { class: 'jr-stat-label' }, s.label || ''),
                h('span', { class: 'jr-stat-value' }, String(s.value ?? '')),
                s.change != null
                    ? h('span', { class: `jr-stat-change ${trendClass}` }, [
                        trendIcon,
                        String(s.change),
                    ])
                    : null,
            ]);
        };
    },
});

// ──── Tabs (标签页) ────

const JrTabs = defineComponent({
    name: 'JrTabs',
    props: { spec: Object },
    setup(props) {
        const activeTab = ref(null);

        return () => {
            const s = props.spec || {};
            const tabs = s.tabs || [];
            if (!activeTab.value && tabs.length > 0) {
                activeTab.value = s.defaultTab || tabs[0]?.key || tabs[0]?.label || '';
            }
            const children = s.children || [];

            return h('div', { class: 'jr-root jr-tabs' }, [
                h('div', { class: 'jr-tabs-header' }, tabs.map(tab => {
                    const key = tab.key || tab.label || '';
                    const isActive = key === activeTab.value;
                    return h('button', {
                        key,
                        class: `jr-tab-btn ${isActive ? 'active' : ''}`,
                        onClick: () => { activeTab.value = key; },
                    }, tab.label || key);
                })),
                h('div', { class: 'jr-tabs-body' },
                    renderChildren(
                        children.filter((_, i) => {
                            const tab = tabs[i];
                            return tab && (tab.key || tab.label) === activeTab.value;
                        }),
                        getRenderer(),
                    ),
                ),
            ]);
        };
    },
});

// ──── Accordion (折叠面板) ────

const JrAccordion = defineComponent({
    name: 'JrAccordion',
    props: { spec: Object },
    setup(props) {
        const isOpen = ref(false);

        return () => {
            const s = props.spec || {};
            if (isOpen.value === false && s.open === true) isOpen.value = true;

            return h('div', { class: `jr-root jr-accordion ${isOpen.value ? 'jr-accordion-open' : ''}` }, [
                h('button', {
                    class: 'jr-accordion-trigger',
                    onClick: () => { isOpen.value = !isOpen.value; },
                }, [
                    h('span', { class: 'jr-accordion-arrow' }, isOpen.value ? '▾' : '▸'),
                    h('span', null, s.title || ''),
                ]),
                isOpen.value
                    ? h('div', { class: 'jr-accordion-body' },
                        renderChildren(s.children || [], getRenderer()))
                    : null,
            ]);
        };
    },
});

// ──── Timeline (时间线) ────

const JrTimeline = defineComponent({
    name: 'JrTimeline',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const items = s.items || [];
            return h('div', { class: 'jr-root jr-timeline' }, items.map((item, i) => {
                const status = item.status || 'pending';
                const dotClass = status === 'done' ? 'jr-dot-done'
                    : status === 'active' ? 'jr-dot-active' : 'jr-dot-pending';
                const isLast = i === items.length - 1;
                return h('div', { key: i, class: 'jr-timeline-item' }, [
                    h('div', { class: 'jr-timeline-dot-col' }, [
                        h('div', { class: `jr-timeline-dot ${dotClass}` }),
                        !isLast ? h('div', { class: 'jr-timeline-line' }) : null,
                    ]),
                    h('div', { class: 'jr-timeline-content' }, [
                        h('div', { class: 'jr-timeline-head' }, [
                            h('strong', null, item.title || ''),
                            item.time ? h('span', { class: 'jr-timeline-time' }, item.time) : null,
                        ]),
                        item.description
                            ? h('p', { class: 'jr-timeline-desc' }, item.description)
                            : null,
                    ]),
                ]);
            }));
        };
    },
});

// ──── Button (按钮) ────

const JrButton = defineComponent({
    name: 'JrButton',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            const variant = s.variant || 'default';
            return h('button', {
                class: `jr-root jr-button jr-button-${variant}`,
                disabled: !!s.disabled,
            }, s.label || '');
        };
    },
});

// ──── Image (图片) ────

const JrImage = defineComponent({
    name: 'JrImage',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            return h('figure', { class: 'jr-root jr-image' }, [
                h('img', {
                    class: 'jr-image-img',
                    src: s.src || '',
                    alt: s.alt || '',
                    style: s.width ? { maxWidth: s.width } : {},
                }),
                s.caption ? h('figcaption', { class: 'jr-image-caption' }, s.caption) : null,
            ]);
        };
    },
});

// ──── Link (链接) ────

const JrLink = defineComponent({
    name: 'JrLink',
    props: { spec: Object },
    setup(props) {
        return () => {
            const s = props.spec || {};
            return h('a', {
                class: 'jr-root jr-link',
                href: s.href || '#',
                target: '_blank',
                rel: 'noopener noreferrer',
            }, s.text || s.href || '');
        };
    },
});

// ──── Widget 注册表 ────

export const WIDGET_REGISTRY = {
    Card: { component: JrCard },
    Metric: { component: JrMetric },
    Stat: { component: JrStat },
    Stack: { component: JrStack },
    Heading: { component: JrHeading },
    Table: { component: JrTable },
    Tabs: { component: JrTabs },
    Accordion: { component: JrAccordion },
    Timeline: { component: JrTimeline },
    Alert: { component: JrAlert },
    Badge: { component: JrBadge },
    CodeBlock: { component: JrCodeBlock },
    List: { component: JrList },
    Progress: { component: JrProgress },
    Separator: { component: JrSeparator },
    Text: { component: JrText },
    Markdown: { component: JrMarkdown },
    Chart: { component: JrChart },
    Button: { component: JrButton },
    Image: { component: JrImage },
    Link: { component: JrLink },
};

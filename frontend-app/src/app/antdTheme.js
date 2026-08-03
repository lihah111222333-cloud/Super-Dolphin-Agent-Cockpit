import { theme as antdThemeAlgorithms } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import enUS from 'antd/locale/en_US';
import { APPEARANCE_ACCENT_TOKENS } from './appearance/appearanceSchema.js';

/**
 * Ant Design / Ant Design X 主题映射。
 *
 * 这里的颜色不是第二套独立色板：每个值都与 styles.css 的
 * `:root[data-theme="dark"|"light"]` 以及 AppShell.css 的
 * `.sa-window.suiyuan-shell[data-theme="dark"]` 中的 CSS Token 一一对应。
 * 修改任一侧色值时，必须同步另一侧，并同步 styles.test.js 的契约断言。
 *
 * 深色方向：深石墨底 + 蓝紫主色（AI Command Center），三级表面层次：
 * colorBgBase(底层) < colorBgContainer(面板) < colorBgElevated(浮起)。
 * 浅色方向：冷白底 + 同族蓝紫（Airy Command Center）。
 */
const FONT_FAMILY = 'Inter, Outfit, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Segoe UI", sans-serif';

/** 动效节奏与 styles.css 的 --motion-* token 对齐（140–220ms 主区间）。 */
const MOTION_TOKENS = Object.freeze({
  motionDurationFast: '0.14s',
  motionDurationMid: '0.2s',
  motionDurationSlow: '0.22s',
});

/** 深色 token：对应 styles.css `:root[data-theme="dark"]`。 */
const DARK_TOKENS = Object.freeze({
  colorPrimary: '#8f9ff8', // --primary
  colorInfo: '#8f9ff8',
  colorSuccess: '#7fd8a1', // --success
  colorWarning: '#e8c177', // --warning
  colorError: '#ffb4ab', // --error
  colorBgBase: '#0e1014', // --bg
  colorBgLayout: '#0e1014', // --app-bg
  colorBgContainer: '#161920', // --surface
  colorBgElevated: '#232833', // --surface-3（浮起面板/弹层）
  colorBgSpotlight: '#2a303d', // Tooltip/聚焦浮层 ≈ --suiyuan-surface-highest
  colorText: '#e9ebf2', // --text-pri
  colorTextSecondary: '#b8bfcc', // --text-sec
  colorTextTertiary: '#8b93a3', // --text-muted
  colorBorder: '#2b313e', // --border
  colorBorderSecondary: '#232833', // --surface-3，低对比分隔
  borderRadius: 8, // --radius
  fontFamily: FONT_FAMILY,
  controlHeight: 34,
  boxShadow: '0 24px 72px rgba(0, 0, 0, 0.38)', // --shadow
  boxShadowSecondary: '0 8px 30px rgba(0, 0, 0, 0.2)', // --suiyuan-input-shadow
  ...MOTION_TOKENS,
});

/** 深色组件级覆盖：只调整与现有工作台视觉语言相关的组件。 */
const DARK_COMPONENTS = Object.freeze({
  Button: Object.freeze({
    controlHeight: 34,
    fontWeight: 600,
    primaryShadow: 'none',
  }),
  Input: Object.freeze({
    colorBgContainer: '#1b1f28', // --surface-2
    hoverBorderColor: '#495161', // --border-strong
    activeBorderColor: '#8f9ff8',
  }),
  Select: Object.freeze({
    colorBgContainer: '#1b1f28',
    optionSelectedBg: 'rgba(143, 159, 248, 0.14)', // --primary-soft
  }),
  Card: Object.freeze({
    colorBgContainer: '#1b1f28', // 普通面板
  }),
  Modal: Object.freeze({
    contentBg: '#1b1f28',
    headerBg: '#1b1f28',
  }),
  Drawer: Object.freeze({
    colorBgElevated: '#1b1f28',
  }),
  Menu: Object.freeze({
    itemBg: 'transparent',
    subMenuItemBg: 'transparent',
    itemSelectedBg: 'rgba(143, 159, 248, 0.14)',
    itemSelectedColor: '#e9ebf2',
  }),
  Tooltip: Object.freeze({
    colorBgDefault: '#2a303d',
  }),
  Tabs: Object.freeze({
    itemColor: '#b8bfcc',
    itemHoverColor: '#e9ebf2',
    itemSelectedColor: '#e9ebf2',
    inkBarColor: '#8f9ff8',
  }),
  Segmented: Object.freeze({
    trackBg: '#161920',
    itemColor: '#b8bfcc',
    itemSelectedBg: '#2a303d',
    itemSelectedColor: '#e9ebf2',
    itemHoverBg: 'rgba(233, 235, 242, 0.06)',
  }),
});

/** 浅色 token：对应 styles.css `:root[data-theme="light"]`（Airy Command Center 冷白 + 蓝紫）。 */
const LIGHT_TOKENS = Object.freeze({
  colorPrimary: '#4f5dd8', // --primary
  colorInfo: '#54627a', // --info
  colorSuccess: '#128b52', // --success
  colorWarning: '#b45309', // --warning
  colorError: '#ba1a1a', // --error
  colorBgBase: '#f5f7fa', // --bg
  colorBgLayout: '#f5f7fa', // --app-bg
  colorBgContainer: '#ffffff', // --surface
  colorBgElevated: '#ffffff', // --surface-lowest
  colorBgSpotlight: '#2a2e3a', // --inverse-surface，深底 tooltip
  colorText: '#171a23', // --text-pri
  colorTextSecondary: '#4c5568', // --text-sec
  colorTextTertiary: '#8a93a8', // --text-muted
  colorBorder: '#d3d9e5', // --border
  colorBorderSecondary: '#e9edf4', // --surface-3
  borderRadius: 8,
  fontFamily: FONT_FAMILY,
  controlHeight: 34,
  boxShadow: '0 22px 70px rgba(30, 38, 68, 0.12)', // --shadow
  boxShadowSecondary: '0 8px 30px rgba(30, 38, 68, 0.06)', // --suiyuan-input-shadow
  ...MOTION_TOKENS,
});

const LIGHT_COMPONENTS = Object.freeze({
  Button: Object.freeze({
    controlHeight: 34,
    fontWeight: 600,
    primaryShadow: 'none',
  }),
  Input: Object.freeze({
    colorBgContainer: '#f2f4f9', // --surface-2
    hoverBorderColor: '#8a93a8', // --border-strong
    activeBorderColor: '#4f5dd8',
  }),
  Select: Object.freeze({
    colorBgContainer: '#f2f4f9',
    optionSelectedBg: 'rgba(79, 93, 216, 0.1)', // --primary-soft
  }),
  Card: Object.freeze({
    colorBgContainer: '#ffffff',
  }),
  Modal: Object.freeze({
    contentBg: '#ffffff',
    headerBg: '#ffffff',
  }),
  Menu: Object.freeze({
    itemBg: 'transparent',
    subMenuItemBg: 'transparent',
    itemSelectedBg: 'rgba(79, 93, 216, 0.1)',
    itemSelectedColor: '#171a23',
  }),
  Tabs: Object.freeze({
    itemColor: '#4c5568',
    itemHoverColor: '#171a23',
    itemSelectedColor: '#171a23',
    inkBarColor: '#4f5dd8',
  }),
  Segmented: Object.freeze({
    trackBg: '#eceff5',
    itemColor: '#4c5568',
    itemSelectedBg: '#ffffff',
    itemSelectedColor: '#171a23',
  }),
});

/**
 * 按当前应用主题生成 antd ConfigProvider / XProvider 共用的 ThemeConfig。
 * @param {string} theme COLOR_THEMES.dark | COLOR_THEMES.light
 * @returns {import('antd').ThemeConfig}
 */
export function antdThemeConfig(theme, accent = 'violet') {
  if (theme !== 'dark' && theme !== 'light') throw new Error('Ant Design theme must be light or dark');
  const accentTokens = APPEARANCE_ACCENT_TOKENS[accent];
  if (!accentTokens) throw new Error(`Ant Design accent tokens missing for ${accent}`);
  const dark = theme === 'dark';
  const colorPrimary = accentTokens[theme];
  const baseComponents = dark ? DARK_COMPONENTS : LIGHT_COMPONENTS;
  return {
    algorithm: dark ? antdThemeAlgorithms.darkAlgorithm : antdThemeAlgorithms.defaultAlgorithm,
    token: {
      ...(dark ? DARK_TOKENS : LIGHT_TOKENS),
      colorInfo: colorPrimary,
      colorPrimary,
    },
    components: {
      ...baseComponents,
      Input: { ...baseComponents.Input, activeBorderColor: colorPrimary },
      Tabs: { ...baseComponents.Tabs, inkBarColor: colorPrimary },
    },
  };
}

/**
 * 应用语言（'zh' | 'en'）到 antd locale 的映射。
 * @param {string} locale
 */
export function antdLocaleFor(locale) {
  return locale === 'en' ? enUS : zhCN;
}

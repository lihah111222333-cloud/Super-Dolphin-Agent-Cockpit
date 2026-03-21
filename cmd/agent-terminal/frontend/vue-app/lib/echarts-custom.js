/**
 * ECharts 按需引入定制模块。
 *
 * 只注册常用的图表类型和组件, 避免打包完整 ECharts (1.0 MB → ~300 KB)。
 * 如果 JrChart 渲染时遇到组件缺失警告, 在这里追加相应 import 即可。
 */
import * as echarts from 'echarts/core';

// ── 图表类型 (仅保留高频类型) ──
import { BarChart, LineChart, PieChart, ScatterChart } from 'echarts/charts';

// ── 组件 (仅保留核心组件) ──
import {
    GridComponent,
    TooltipComponent,
    LegendComponent,
    TitleComponent,
    DatasetComponent,
    TransformComponent,
} from 'echarts/components';

// ── 渲染器 ──
import { CanvasRenderer } from 'echarts/renderers';

// ── 注册 ──
echarts.use([
    BarChart, LineChart, PieChart, ScatterChart,
    GridComponent, TooltipComponent, LegendComponent,
    TitleComponent, DatasetComponent, TransformComponent,
    CanvasRenderer,
]);

export default echarts;

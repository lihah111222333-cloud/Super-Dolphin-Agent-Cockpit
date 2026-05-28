import { computed, ref, watch } from '../../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../../services/api.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '';
}

function userRunsErrorText(error) {
  return error ? '无法加载运行历史，请稍后重试。' : '';
}

function finalOutputFileErrorText(error) {
  return error ? '无法读取最终结果文件，请稍后重试。' : '';
}

function previewValue(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') {
    const text = value.trim();
    return text.length > 520 ? `${text.slice(0, 520)}...` : text;
  }
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return JSON.stringify(value, null, 2);
}

const OUTPUT_KIND_LABELS = {
  file: '文件',
  sharedfile: '文件',
  shared_file: '文件',
  text: '文本',
  json: '数据',
};

function outputKindLabel(value) {
  const kind = textValue(value);
  if (!kind) return '-';
  return OUTPUT_KIND_LABELS[kind.toLowerCase()] || kind;
}

export const DagFinalOutputPanel = {
  name: 'DagFinalOutputPanel',
  props: {
    finalOutput: { type: Object, default: null },
    runsError: { type: [String, Object], default: null },
  },
  setup(props) {
    let readSeq = 0;
    const fileContent = ref('');
    const fileError = ref(null);
    const reading = ref(false);
    const outputPath = computed(() => {
      const output = props.finalOutput || {};
      return textValue(output.path, output.sharedfile?.path, output.sharedFile?.path, output.shared_file?.path);
    });
    const outputKind = computed(() => outputKindLabel(props.finalOutput?.kind || props.finalOutput?.type));
    const runsErrorText = computed(() => userRunsErrorText(props.runsError));
    const fileErrorText = computed(() => finalOutputFileErrorText(fileError.value));
    const previewText = computed(() => {
      const output = props.finalOutput || {};
      return previewValue(output.text)
        || previewValue(output.content)
        || previewValue(output.result)
        || previewValue(output.value)
        || (props.finalOutput && !outputPath.value ? '已生成最终结果，暂不支持预览。' : '');
    });

    watch(outputPath, () => {
      readSeq += 1;
      fileContent.value = '';
      fileError.value = null;
      reading.value = false;
    });

    async function readFile() {
      const path = outputPath.value;
      if (!path) return;
      const seq = readSeq + 1;
      readSeq = seq;
      reading.value = true;
      fileError.value = null;
      try {
        const detail = await callAPI('ui/memory/shared-file/get', { path });
        if (!detail || typeof detail !== 'object') {
          throw new Error('无法读取最终结果文件：返回内容为空');
        }
        if (seq !== readSeq || path !== outputPath.value) return;
        fileContent.value = (detail.content || '').toString();
      } catch (error) {
        if (seq !== readSeq || path !== outputPath.value) return;
        fileError.value = error;
      } finally {
        if (seq === readSeq) reading.value = false;
      }
    }

    function openFile() {
      return readFile();
    }

    return {
      fileContent,
      fileErrorText,
      outputKind,
      outputPath,
      previewText,
      reading,
      readFile,
      openFile,
      runsErrorText,
    };
  },
  template: `
    <section class="dag-detail-section dag-final-output-panel" data-testid="dag-final-output-panel">
      <div class="dag-section-title">最终结果</div>
      <div v-if="runsErrorText" class="dag-console-error-inline" data-testid="dag-runs-error">{{ runsErrorText }}</div>
      <div v-else>
        <div v-if="outputPath" class="dag-final-file">
          <div class="dag-final-kind">{{ outputKind }}</div>
          <code>{{ outputPath }}</code>
          <div class="dag-final-actions">
            <button type="button" class="btn btn-ghost" data-method="ui/memory/shared-file/get" data-testid="dag-final-output-open" :disabled="reading" @click="openFile">打开</button>
            <button type="button" class="btn btn-ghost" data-method="ui/memory/shared-file/get" data-testid="dag-final-output-read" :disabled="reading" @click="readFile">读取</button>
          </div>
          <div v-if="fileErrorText" class="dag-console-error-inline" data-testid="dag-final-output-error">{{ fileErrorText }}</div>
          <pre v-if="fileContent" class="dag-final-preview" data-testid="dag-final-output-content">{{ fileContent }}</pre>
        </div>
        <pre v-else-if="finalOutput" class="dag-final-preview" data-testid="dag-final-output-preview">{{ previewText }}</pre>
        <div v-else class="dag-console-muted" data-testid="dag-final-output-empty">当前运行尚未标记最终结果。</div>
      </div>
    </section>
  `,
};

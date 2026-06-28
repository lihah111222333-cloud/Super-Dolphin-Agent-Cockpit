import { computed } from '../../lib/vue.esm-browser.prod.js';
import { logDebug } from '../services/log.js';

export const ProjectModal = {
  name: 'ProjectModal',
  props: {
    store: { type: Object, required: true },
  },
  setup(props) {
    const canConfirm = computed(() => Boolean((props.store.state.modalPath || '').trim()));
    function closeByMask() {
      logDebug('ui', 'projectModal.mask.close', {});
      props.store.closeModal();
    }

    function onConfirm() {
      logDebug('ui', 'projectModal.confirm.click', {
        path: props.store.state.modalPath || '',
      });
      props.store.confirmModal();
    }

    function onBrowse() {
      logDebug('ui', 'projectModal.browse.click', {});
      props.store.browseDirectory();
    }

    return {
      canConfirm,
      closeByMask,
      onConfirm,
      onBrowse,
    };
  },
  template: `
    <div v-if="store.state.showModal" class="modal-overlay" @click.self="closeByMask">
      <div class="modal-box">
        <div class="modal-title">添加项目</div>
        <div class="modal-input-row">
          <input
            v-model="store.state.modalPath"
            class="modal-input modal-input-flex"
            type="text"
            placeholder="/Users/you/projects/my-app"
            spellcheck="false"
            autocomplete="off"
            @keydown.enter="onConfirm"
            @keydown.esc="closeByMask"
          />
          <button class="btn btn-secondary modal-browse-btn" @click="onBrowse" :disabled="store.state.browsing">
            {{ store.state.browsing ? '打开中...' : '浏览...' }}
          </button>
        </div>
        <div class="modal-btns">
          <button class="btn btn-ghost" @click="closeByMask">取消</button>
          <button class="btn btn-primary" @click="onConfirm" :disabled="!canConfirm">确定</button>
        </div>
      </div>
    </div>
  `,
};

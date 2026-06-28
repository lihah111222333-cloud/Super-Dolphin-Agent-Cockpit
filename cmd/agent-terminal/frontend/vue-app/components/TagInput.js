// TagInput.js — 场景标签输入组件 (chips + input)
import { h, defineComponent, ref, computed } from '../../lib/vue.esm-browser.prod.js';

export const TagInput = defineComponent({
  name: 'TagInput',
  props: {
    modelValue: { type: Array, default: () => [] },
    placeholder: { type: String, default: '输入标签后按回车' },
    disabled: { type: Boolean, default: false },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    const inputText = ref('');
    const inputRef = ref(null);

    const remainingPlaceholder = computed(() =>
      props.modelValue.length > 0 ? '' : props.placeholder,
    );

    function focusInput() {
      if (inputRef.value) inputRef.value.focus();
    }

    function add() {
      const raw = inputText.value.replace(/[,，]/g, '').trim();
      if (!raw) { inputText.value = ''; return; }
      const tags = props.modelValue;
      if (tags.some((t) => t.trim() === raw)) { inputText.value = ''; return; }
      emit('update:modelValue', [...tags, raw]);
      inputText.value = '';
    }

    function remove(tag) {
      if (props.disabled) return;
      emit('update:modelValue', props.modelValue.filter((t) => t !== tag));
    }

    return () =>
      h(
        'div',
        {
          class: ['sp-tag-input', { disabled: props.disabled }],
          onClick: focusInput,
        },
        [
          ...props.modelValue.map((tag) =>
            h('span', { class: 'sp-tag-chip', key: tag }, [
              tag,
              h(
                'button',
                {
                  class: 'sp-tag-remove',
                  disabled: props.disabled,
                  onClick: (e) => { e.stopPropagation(); remove(tag); },
                },
                '×',
              ),
            ]),
          ),
          h('input', {
            ref: inputRef,
            value: inputText.value,
            onInput: (e) => { inputText.value = e.target.value; },
            onKeydown: (e) => {
              if (e.key === 'Enter') { e.preventDefault(); add(); }
              if (e.key === ',' || e.key === '，') { e.preventDefault(); add(); }
            },
            placeholder: remainingPlaceholder.value,
            disabled: props.disabled,
            class: 'sp-tag-input-field',
          }),
        ],
      );
  },
});

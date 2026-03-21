import { computed } from '../../../lib/vue.esm-browser.prod.js';

type AttachmentItem = {
  kind?: string;
  name?: string;
  path?: string;
  previewUrl?: string;
};

type AttachmentPreviewApi = {
  attachmentLabel?: (att: AttachmentItem) => string;
  attachmentPreview?: (att: AttachmentItem) => string;
  attachmentType?: (att: AttachmentItem) => string;
  imageAttachments?: (attachments: AttachmentItem[]) => AttachmentItem[];
  fileAttachments?: (attachments: AttachmentItem[]) => AttachmentItem[];
  onAttachmentHoverMove?: (event: Event, att: AttachmentItem) => void;
  onAttachmentHoverLeave?: () => void;
  openAttachmentLightbox?: (att: AttachmentItem) => void;
};

type AttachmentPreviewProps = {
  attachments?: AttachmentItem[];
  attachmentApi?: AttachmentPreviewApi;
};

function setupAttachmentPreview(props: AttachmentPreviewProps) {
  const imageList = computed(() => props.attachmentApi?.imageAttachments?.(props.attachments || []) || []);
  const fileList = computed(() => props.attachmentApi?.fileAttachments?.(props.attachments || []) || []);

  function attachmentLabel(att: AttachmentItem): string {
    return props.attachmentApi?.attachmentLabel?.(att) || '';
  }

  function attachmentPreview(att: AttachmentItem): string {
    return props.attachmentApi?.attachmentPreview?.(att) || '';
  }

  function attachmentType(att: AttachmentItem): string {
    return props.attachmentApi?.attachmentType?.(att) || '';
  }

  function onHoverMove(event: Event, att: AttachmentItem): void {
    props.attachmentApi?.onAttachmentHoverMove?.(event, att);
  }

  function onHoverLeave(): void {
    props.attachmentApi?.onAttachmentHoverLeave?.();
  }

  function openLightbox(att: AttachmentItem): void {
    props.attachmentApi?.openAttachmentLightbox?.(att);
  }

  return {
    imageList,
    fileList,
    attachmentLabel,
    attachmentPreview,
    attachmentType,
    onHoverMove,
    onHoverLeave,
    openLightbox,
  };
}

export const AttachmentPreview = {
  name: 'AttachmentPreview',
  props: {
    attachments: { type: Array, default: () => [] },
    attachmentApi: { type: Object, default: () => ({}) },
  },
  setup: setupAttachmentPreview,
  template: `
    <div v-if="imageList.length > 0" class="chat-attachment-gallery">
      <button
        v-for="(att, idx) in imageList"
        :key="'img-' + ((att.path || att.name || '') + '-' + idx)"
        class="chat-attachment-card"
        type="button"
        :title="attachmentLabel(att)"
        @mouseenter="onHoverMove($event, att)"
        @mousemove="onHoverMove($event, att)"
        @mouseleave="onHoverLeave"
        @focus="onHoverMove($event, att)"
        @blur="onHoverLeave"
        @click="openLightbox(att)"
      >
        <img
          class="chat-attachment-card__image"
          :src="attachmentPreview(att)"
          :alt="attachmentLabel(att)"
          loading="lazy"
        />
        <span class="chat-attachment-card__meta">
          <span class="attachment-kind">IMG</span>
          <span class="chat-attachment-card__name">{{ attachmentLabel(att) }}</span>
        </span>
      </button>
    </div>
    <div v-if="fileList.length > 0" class="chat-attachment-list">
      <span
        v-for="(att, idx) in fileList"
        :key="'file-' + ((att.path || att.name || '') + '-' + idx)"
        class="chat-attachment-pill"
      >
        <span class="attachment-kind">{{ attachmentType(att) }}</span>
        <span class="attachment-name">{{ attachmentLabel(att) }}</span>
      </span>
    </div>
  `,
};

import { firstText, textValue } from './markdownMessageModel.js';

function CitationChip({ chip, actions }) {
  return (
    <button
      type="button"
      className={`chat-md-citation ${textValue(chip.className)}`.trim()}
      title={firstText(chip.title, chip.displayLabel)}
      onClick={() => actions?.onCitation?.(chip.payload)}
    >
      {chip.icon ? <span className="chat-md-citation__icon" aria-hidden="true">{chip.icon}</span> : null}
      <span className="chat-md-citation__body">
        <span className="chat-md-citation__label">{chip.displayLabel}</span>
      </span>
    </button>
  );
}

function MarkdownDirectiveChip({ chip, actions }) {
  if (!chip) return null;
  if (chip.type === 'text') return textValue(chip.text);
  if (chip.type === 'file') {
    return (
      <button
        type="button"
        className="chat-md-file-ref chat-md-file-citation"
        aria-label={`\u6253\u5f00\u6587\u4ef6\u5f15\u7528 ${chip.filePath}`}
        title={firstText(chip.title, chip.filePath)}
        onClick={() => actions?.onFileRef?.(chip.payload)}
      >
        {chip.display}
      </button>
    );
  }
  return <CitationChip chip={chip} actions={actions} />;
}

function MarkdownCitationLinkChip({ chip, actions }) {
  if (!chip) return null;
  return <CitationChip chip={chip} actions={actions} />;
}

export { MarkdownCitationLinkChip, MarkdownDirectiveChip };

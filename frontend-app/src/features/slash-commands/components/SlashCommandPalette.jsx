import {
  Command,
  MessageSquareText,
  Sparkles,
  Workflow,
  Wrench,
} from 'lucide-react';
import {
  SLASH_COMMAND_KIND_ORDER,
  slashCommandOptionId,
} from '../model/slashCommandModel.js';
import './SlashCommandPalette.css';

const CATEGORY_ICONS = Object.freeze({
  builtin: Command,
  skill: Sparkles,
  prompt: MessageSquareText,
  automation: Workflow,
  mcp_tool: Wrench,
});

function categoryItems(items, kind) {
  return items.filter((item) => item.kind === kind);
}

function categoryStatusText(state, copy, cwd) {
  if (state.status === 'disabled') return cwd ? copy.loading : copy.projectRequired;
  if (state.status === 'loading') return copy.loading;
  if (state.status === 'error') return `${copy.loadError}: ${state.error}`;
  return '';
}

function liveStatusText(categoryStates, copy, cwd, selecting) {
  if (selecting) return copy.selecting;
  for (const kind of SLASH_COMMAND_KIND_ORDER) {
    const status = categoryStatusText(categoryStates[kind], copy, cwd);
    if (status) return status;
  }
  return '';
}

function PaletteOption({ activeId, item, selectItem, setActiveId }) {
  const selected = activeId === item.id;
  return (
    <button
      type="button"
      id={slashCommandOptionId(item)}
      className={`slash-command-palette__option${selected ? ' is-active' : ''}`}
      role="option"
      aria-disabled={item.disabled ? 'true' : 'false'}
      aria-selected={selected ? 'true' : 'false'}
      onClick={() => {
        if (!item.disabled) void selectItem(item);
      }}
      onMouseEnter={() => setActiveId(item.id)}
    >
      <span className="slash-command-palette__command">/{item.name}</span>
      <span className="slash-command-palette__label">{item.label}</span>
      {item.description ? (
        <span className="slash-command-palette__description">{item.description}</span>
      ) : null}
      {item.disabled && item.disabledReason ? (
        <span className="slash-command-palette__reason">{item.disabledReason}</span>
      ) : null}
    </button>
  );
}

function PaletteCategory(props) {
  const { activeId, copy, cwd, items, kind, selectItem, setActiveId, state } = props;
  const Icon = CATEGORY_ICONS[kind];
  const status = categoryStatusText(state, copy, cwd);
  if (items.length === 0 && !status) return null;
  return (
    <div className="slash-command-palette__category" role="presentation">
      <div className="slash-command-palette__category-title" role="presentation">
        <Icon aria-hidden="true" size={15} strokeWidth={1.8} />
        <span>{copy.categories[kind]}</span>
      </div>
      {status ? (
        <div className={`slash-command-palette__state is-${state.status}`} role="presentation">
          {status}
        </div>
      ) : null}
      {items.map((item) => (
        <PaletteOption
          key={item.id}
          activeId={activeId}
          item={item}
          selectItem={selectItem}
          setActiveId={setActiveId}
        />
      ))}
    </div>
  );
}

export function SlashCommandPalette(props) {
  const {
    activeId,
    categoryStates,
    copy,
    cwd,
    items,
    listboxId,
    open,
    selectItem,
    selecting,
    setActiveId,
  } = props;
  if (!open) return null;
  const allSuccessful = Object.values(categoryStates)
    .every((state) => state.status === 'success');
  const liveStatus = liveStatusText(categoryStates, copy, cwd, selecting);
  return (
    <div className="slash-command-palette" data-selecting={selecting ? 'true' : 'false'}>
      <div
        id={listboxId}
        className="slash-command-palette__results"
        role="listbox"
        aria-label={copy.ariaLabel}
      >
        {SLASH_COMMAND_KIND_ORDER.map((kind) => (
          <PaletteCategory
            key={kind}
            activeId={activeId}
            copy={copy}
            cwd={cwd}
            items={categoryItems(items, kind)}
            kind={kind}
            selectItem={selectItem}
            setActiveId={setActiveId}
            state={categoryStates[kind]}
          />
        ))}
        {items.length === 0 && allSuccessful ? (
          <div className="slash-command-palette__empty" role="presentation">
            {copy.noResults}
          </div>
        ) : null}
      </div>
      <div className="slash-command-palette__live" role="status" aria-live="polite">
        {liveStatus}
      </div>
    </div>
  );
}

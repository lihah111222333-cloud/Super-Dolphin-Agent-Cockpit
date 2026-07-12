import React, { useId, useMemo, useState } from 'react';

import { FocusTrapDialog } from '../../../shared/ui/FocusTrapDialog.jsx';
import {
  filterCommandPaletteItems,
  groupCommandPaletteItems,
  projectCommandPaletteItems,
} from '../model/commandPaletteModel.js';
import './CommandPalette.css';

function nextPaletteIndex(key, activeIndex, itemCount) {
  if (itemCount === 0) return 0;
  if (key === 'Home') return 0;
  if (key === 'End') return itemCount - 1;
  if (key === 'ArrowDown') return (activeIndex + 1) % itemCount;
  if (key === 'ArrowUp') return (activeIndex - 1 + itemCount) % itemCount;
  return activeIndex;
}

function executePaletteItem(item, execute, onClose) {
  if (!item || item.disabled) return;
  const result = execute(item.id);
  if (result.executed) onClose();
}

function CommandPaletteOption(options) {
  const { execute, item, itemIndex, onClose, optionId, selectedIndex, setActiveIndex } = options;
  return (
    <button
      id={optionId}
      type="button"
      role="option"
      aria-disabled={item.disabled}
      aria-selected={itemIndex === selectedIndex}
      className="command-palette__option"
      onClick={() => executePaletteItem(item, execute, onClose)}
      onMouseEnter={() => setActiveIndex(itemIndex)}
    >
      <span className="command-palette__option-copy">
        <strong>{item.label}</strong>
        {item.help ? <small>{item.help}</small> : null}
        {item.disabledReason ? <small className="command-palette__disabled-reason">{item.disabledReason}</small> : null}
      </span>
      <kbd>{item.shortcut.key}</kbd>
    </button>
  );
}

function CommandPaletteGroup(options) {
  const { execute, group, groupId, itemIndexById, onClose, optionIdByItemId, selectedIndex, setActiveIndex } = options;
  const labelId = `${groupId}-label`;
  return (
    <section className="command-palette__section" role="group" aria-labelledby={labelId}>
      <h3 id={labelId}>{group.label}</h3>
      {group.items.map((item) => (
        <CommandPaletteOption
          execute={execute}
          item={item}
          itemIndex={itemIndexById.get(item.id)}
          key={item.id}
          onClose={onClose}
          optionId={optionIdByItemId.get(item.id)}
          selectedIndex={selectedIndex}
          setActiveIndex={setActiveIndex}
        />
      ))}
    </section>
  );
}

function CommandPaletteList(options) {
  const { copy, execute, groups, itemIndexById, listboxId, onClose, optionIdByItemId, selectedIndex, setActiveIndex } = options;
  return (
    <div id={listboxId} className="command-palette__list" role="listbox" aria-label={copy.title}>
      {groups.map((group, groupIndex) => (
        <CommandPaletteGroup
          execute={execute}
          group={group}
          groupId={`${listboxId}-group-${groupIndex}`}
          itemIndexById={itemIndexById}
          key={group.section}
          onClose={onClose}
          optionIdByItemId={optionIdByItemId}
          selectedIndex={selectedIndex}
          setActiveIndex={setActiveIndex}
        />
      ))}
    </div>
  );
}

function CommandPaletteBody({ commands, execute, onClose, copy }) {
  const paletteId = useId();
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndex] = useState(0);
  const projectedItems = useMemo(() => projectCommandPaletteItems(commands, copy), [commands, copy]);
  const items = useMemo(() => filterCommandPaletteItems(projectedItems, query), [projectedItems, query]);
  const groups = useMemo(() => groupCommandPaletteItems(items), [items]);
  const itemIndexById = useMemo(() => new Map(items.map((item, index) => [item.id, index])), [items]);
  const selectedIndex = items.length === 0 ? 0 : Math.min(activeIndex, items.length - 1);
  const listboxId = `${paletteId}-listbox`;
  const optionIdByItemId = useMemo(
    () => new Map(projectedItems.map((item, index) => [item.id, `${paletteId}-option-${index}`])),
    [paletteId, projectedItems],
  );
  const activeOptionId = items.length === 0 ? undefined : optionIdByItemId.get(items[selectedIndex].id);

  const handleKeyDown = (event) => {
    if (['Home', 'End', 'ArrowDown', 'ArrowUp'].includes(event.key)) {
      event.preventDefault();
      setActiveIndex(nextPaletteIndex(event.key, selectedIndex, items.length));
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      executePaletteItem(items[selectedIndex], execute, onClose);
    }
  };

  return (
    <div className="command-palette__body" onKeyDown={handleKeyDown}>
      <header className="command-palette__header">
        <h2>{copy.title}</h2>
        <input
          type="search"
          role="searchbox"
          aria-activedescendant={activeOptionId}
          aria-controls={items.length === 0 ? undefined : listboxId}
          aria-label={copy.searchPlaceholder}
          placeholder={copy.searchPlaceholder}
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setActiveIndex(0);
          }}
        />
      </header>
      {items.length === 0 ? (
        <p className="command-palette__empty">{copy.empty}</p>
      ) : (
        <CommandPaletteList
          copy={copy}
          execute={execute}
          groups={groups}
          itemIndexById={itemIndexById}
          listboxId={listboxId}
          onClose={onClose}
          optionIdByItemId={optionIdByItemId}
          selectedIndex={selectedIndex}
          setActiveIndex={setActiveIndex}
        />
      )}
    </div>
  );
}

export function CommandPalette({ open, commands, execute, onClose, copy }) {
  if (!open) return null;
  return (
    <FocusTrapDialog
      ariaLabel={copy.title}
      className="command-palette"
      initialFocusSelector="[role=searchbox]"
      onClose={onClose}
    >
      <CommandPaletteBody commands={commands} execute={execute} onClose={onClose} copy={copy} />
    </FocusTrapDialog>
  );
}

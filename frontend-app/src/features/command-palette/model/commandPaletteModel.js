function normalizedSearchText(value) {
  return typeof value === 'string' ? value.trim().toLocaleLowerCase() : '';
}

function requiredLocalizedText(values, key, kind) {
  const value = values?.[key];
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(`missing command ${kind}: ${key}`);
  }
  return value;
}

export function isSubsequenceMatch(value, query) {
  const source = normalizedSearchText(value);
  const needle = normalizedSearchText(query);
  if (needle === '') return true;
  let needleIndex = 0;
  for (const character of source) {
    if (character === needle[needleIndex]) needleIndex += 1;
    if (needleIndex === needle.length) return true;
  }
  return false;
}

export function projectCommandPaletteItems(commands, copy) {
  return Object.freeze(commands.map((command) => {
    const disabled = typeof command.canExecute === 'function' && !command.canExecute();
    const help = command.helpKey
      ? requiredLocalizedText(copy.help, command.helpKey, 'help')
      : '';
    return Object.freeze({
      id: command.id,
      label: requiredLocalizedText(copy.labels, command.labelKey, 'label'),
      help,
      section: command.section,
      sectionLabel: requiredLocalizedText(copy.sections, command.section, 'section'),
      shortcut: command.shortcut,
      disabled,
      disabledReason: disabled && typeof command.disabledReason === 'string' ? command.disabledReason : '',
    });
  }));
}

export function filterCommandPaletteItems(items, query) {
  const needle = normalizedSearchText(query);
  if (needle === '') return items;
  return Object.freeze(items.filter((item) => (
    isSubsequenceMatch(item.label, needle)
    || isSubsequenceMatch(item.help, needle)
    || isSubsequenceMatch(item.id, needle)
  )));
}

export function groupCommandPaletteItems(items) {
  const groups = [];
  const bySection = new Map();
  for (const item of items) {
    let group = bySection.get(item.section);
    if (!group) {
      group = { section: item.section, label: item.sectionLabel, items: [] };
      bySection.set(item.section, group);
      groups.push(group);
    }
    group.items.push(item);
  }
  return Object.freeze(groups.map((group) => Object.freeze({
    ...group,
    items: Object.freeze(group.items),
  })));
}

const RIGHT_PANEL_WIDTH_PATTERN = /^(?:0|[1-9]\d*)(?:\.\d+)?$/;
const RIGHT_PANEL_WIDTH_ERROR_CODE = 'shell_layout.invalid_right_panel_width';

export class ShellLayoutValidationError extends TypeError {
  constructor() {
    super('Invalid right panel width');
    this.name = 'ShellLayoutValidationError';
    this.code = RIGHT_PANEL_WIDTH_ERROR_CODE;
  }
}

function assertRightPanelWidth(value) {
  if (
    typeof value !== 'number'
    || !Number.isFinite(value)
    || value < 0
    || value > Number.MAX_SAFE_INTEGER
  ) {
    throw new ShellLayoutValidationError();
  }
}

function parseRightPanelWidth(storedValue) {
  if (typeof storedValue !== 'string' || !RIGHT_PANEL_WIDTH_PATTERN.test(storedValue)) {
    throw new ShellLayoutValidationError();
  }

  const value = Number(storedValue);
  assertRightPanelWidth(value);
  if (String(value) !== storedValue) {
    throw new ShellLayoutValidationError();
  }
  return value;
}

function serializeRightPanelWidth(value) {
  assertRightPanelWidth(value);
  const storedValue = String(value);
  if (!RIGHT_PANEL_WIDTH_PATTERN.test(storedValue)) {
    throw new ShellLayoutValidationError();
  }
  return storedValue;
}

export const rightPanelWidthSchema = Object.freeze({
  key: 'super-dolphin.shell.right-panel-width',
  initialValue: 380,
  parse: parseRightPanelWidth,
  serialize: serializeRightPanelWidth,
});

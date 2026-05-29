function commandStatusKey(item) {
  if (!item || item.kind !== 'command') return 'done';
  const status = (item.status || '').toString().trim().toLowerCase();
  if (status === 'running') return 'running';
  if (status === 'failed') return 'error';
  if (status === 'canceled' || status === 'cancelled') return 'waiting';
  return 'done';
}

function commandStatusText(item) {
  const key = commandStatusKey(item);
  if (key === 'running') return '命令执行中';
  if (key === 'error') return '命令执行失败';
  if (key === 'waiting') return '命令已取消';
  return '已执行命令';
}

function commandStatusIcon(item) {
  const key = commandStatusKey(item);
  if (key === 'running') return '◌';
  if (key === 'error') return '✕';
  if (key === 'waiting') return '⚠';
  return '✓';
}

function commandStatusIconClass(item) {
  const key = commandStatusKey(item);
  if (key === 'running') return 'ran-command-card__icon--running ran-command-card__icon--spinning';
  if (key === 'error') return 'ran-command-card__icon--error';
  if (key === 'waiting') return 'ran-command-card__icon--waiting';
  return 'ran-command-card__icon--done';
}

function commandTitle(item) {
  const command = (item?.command || '').toString().trim();
  if (!command) return '终端命令';
  return `$ ${command}`;
}

function commandHasOutput(item) {
  return (item?.output || '').toString().length > 0;
}

function commandExitText(item) {
  const code = Number(item?.exitCode);
  if (!Number.isFinite(code)) return '';
  return `退出码 ${Math.trunc(code)}`;
}

export function useCommandHelpers() {
  return {
    commandStatusKey,
    commandStatusText,
    commandStatusIcon,
    commandStatusIconClass,
    commandTitle,
    commandHasOutput,
    commandExitText,
  };
}

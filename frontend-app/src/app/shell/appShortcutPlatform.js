export function appShortcutPlatform() {
  if (typeof navigator === 'undefined') throw new Error('browser shortcut platform is unavailable');
  const browserPlatform = `${String(navigator.platform)} ${String(navigator.userAgent)}`.toLowerCase();
  if (browserPlatform.includes('mac')) return 'darwin';
  if (browserPlatform.includes('win')) return 'win32';
  if (browserPlatform.includes('linux')) return 'linux';
  const runtimePlatform = globalThis.process?.platform;
  if (['darwin', 'linux', 'win32'].includes(runtimePlatform)) return runtimePlatform;
  throw new Error(`unsupported browser shortcut platform: ${browserPlatform || 'unknown'}`);
}

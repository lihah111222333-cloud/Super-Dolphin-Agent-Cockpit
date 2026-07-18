/** @param {unknown} value @param {string} label @returns {string} */
function assertSafeSharedFilePreviewURL(value, label) {
  if (typeof value !== 'string' || !value || value.trim() !== value) {
    throw new TypeError(`${label} must be a canonical loopback preview URL`);
  }
  let parsed;
  try {
    parsed = new URL(value);
  } catch (error) {
    throw new TypeError(`${label} must be a canonical loopback preview URL`, { cause: error });
  }
  const hostname = parsed.hostname.toLowerCase();
  const loopback = hostname === '127.0.0.1' || hostname === '[::1]' || hostname === '::1';
  const port = Number(parsed.port);
  const ids = parsed.searchParams.getAll('id');
  const queryKeys = [...parsed.searchParams.keys()];
  if (
    parsed.href !== value
    || parsed.protocol !== 'http:'
    || !loopback
    || !Number.isInteger(port)
    || port < 1
    || port > 65535
    || parsed.pathname !== '/shared-file-preview'
    || parsed.username
    || parsed.password
    || parsed.hash
    || ids.length !== 1
    || !ids[0].trim()
    || parsed.search !== `?id=${encodeURIComponent(ids[0])}`
    || queryKeys.length !== 1
    || queryKeys[0] !== 'id'
  ) {
    throw new TypeError(`${label} must be a canonical loopback preview URL`);
  }
  return value;
}

export { assertSafeSharedFilePreviewURL };

function normalizedPreviewText(value) {
  return value == null ? '' : String(value);
}

function previewUrlFromResponse(response) {
  const url = normalizedPreviewText(response?.url).trim();
  if (!url) throw new Error('preview URL is empty');
  let parsed;
  try {
    parsed = new URL(url);
  } catch (error) {
    throw new Error('preview URL must be a safe loopback token URL', { cause: error });
  }
  const hostname = parsed.hostname.toLowerCase();
  const loopback = hostname === '127.0.0.1' || hostname === '[::1]' || hostname === '::1';
  const ids = parsed.searchParams.getAll('id');
  const queryKeys = [...parsed.searchParams.keys()];
  if (
    parsed.protocol !== 'http:'
    || !loopback
    || !parsed.port
    || parsed.pathname !== '/shared-file-preview'
    || parsed.username
    || parsed.password
    || parsed.hash
    || ids.length !== 1
    || !ids[0].trim()
    || queryKeys.length !== 1
    || queryKeys[0] !== 'id'
  ) {
    throw new Error('preview URL must be a safe loopback token URL');
  }
  return {
    contentType: normalizedPreviewText(response?.contentType),
    url,
  };
}

export { previewUrlFromResponse };

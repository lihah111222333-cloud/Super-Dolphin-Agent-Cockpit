export function skillNameKey(rawName) {
  return (rawName || '').toString().trim().toLowerCase();
}

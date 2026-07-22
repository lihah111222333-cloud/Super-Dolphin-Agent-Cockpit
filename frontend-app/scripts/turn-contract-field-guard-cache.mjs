export function indexedSourceValue(index, filePath, source, createValue) {
  let sourceValues = index.get(filePath);
  if (!sourceValues) {
    sourceValues = new Map();
    index.set(filePath, sourceValues);
  }
  if (sourceValues.has(source)) return sourceValues.get(source);
  const value = createValue();
  sourceValues.set(source, value);
  return value;
}

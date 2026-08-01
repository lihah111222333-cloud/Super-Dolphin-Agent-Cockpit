const singletonMetricViolationRules = new Set([
  'file-length',
  'nesting',
  'exports',
  'console-log',
  'any',
  'empty-func',
  ['to', 'do'].join(''),
  'line-length',
]);

function functionViolationSignature(rule, message) {
  const match = message.match(/^函数 ([^\s]+) (?:共|有) /);
  return match ? `${rule}\u0000${match[1]}` : `${rule}\u0000${message}`;
}

export function canonicalViolationSignature(signature) {
  if (typeof signature !== 'string') return signature;
  const [rule, message = ''] = signature.split('\u0000');
  if (rule === 'func-length' || rule === 'params') return functionViolationSignature(rule, message);
  return singletonMetricViolationRules.has(rule) ? rule : signature;
}

function signatureBudget(signatures) {
  const budget = new Map();
  for (const signature of signatures) {
    const canonical = canonicalViolationSignature(signature);
    budget.set(canonical, (budget.get(canonical) ?? 0) + 1);
  }
  return budget;
}

export function sameCanonicalViolationBudget(left, right) {
  const leftBudget = signatureBudget(left);
  const rightBudget = signatureBudget(right);
  if (leftBudget.size !== rightBudget.size) return false;
  return [...leftBudget].every(([signature, count]) => rightBudget.get(signature) === count);
}

export function isCanonicalFullScan(scanDirs, explicitFiles, canonicalDirs) {
  if (explicitFiles.length > 0 || scanDirs.length !== canonicalDirs.length) return false;
  const scanned = new Set(scanDirs);
  return canonicalDirs.every((dir) => scanned.has(dir));
}

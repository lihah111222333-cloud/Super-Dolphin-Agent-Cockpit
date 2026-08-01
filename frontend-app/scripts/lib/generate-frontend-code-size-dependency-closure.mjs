import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const scriptRoot = path.dirname(fileURLToPath(import.meta.url));
const appRoot = path.resolve(scriptRoot, '../..');
const outputPath = path.join(scriptRoot, 'frontend-code-size-dependency-closure.json');
const packageLockPath = path.join(appRoot, 'package-lock.json');
const generatorBytes = fs.readFileSync(fileURLToPath(import.meta.url));
const sha256 = (value) => createHash('sha256').update(value).digest('hex');

function fail(message) { throw new Error(message); }
function readJSON(filePath) { return JSON.parse(fs.readFileSync(filePath, 'utf8')); }
function packagePath(name, from) {
  let current = from;
  while (true) {
    const candidate = current ? `${current}/node_modules/${name}` : `node_modules/${name}`;
    if (lock.packages[candidate]) return candidate;
    const index = current.lastIndexOf('/node_modules/');
    if (index < 0) return `node_modules/${name}`;
    current = current.slice(0, index);
  }
}
function closurePackages() {
  const pending = ['node_modules/@babel/parser'];
  const result = new Set();
  while (pending.length > 0) {
    const current = pending.pop();
    if (result.has(current)) continue;
    const record = lock.packages[current];
    if (!record) fail(`package-lock does not contain parser closure package: ${current}`);
    result.add(current);
    for (const name of Object.keys(record.dependencies || {}).sort()) pending.push(packagePath(name, current));
  }
  return [...result].sort();
}
function visitPackageFiles(packagePath, directory, relative, files) {
  for (const entry of fs.readdirSync(directory).sort()) {
    const absolute = path.join(directory, entry);
    const child = relative ? `${relative}/${entry}` : entry;
    const stat = fs.lstatSync(absolute);
    if (stat.isDirectory() && !stat.isSymbolicLink()) visitPackageFiles(packagePath, absolute, child, files);
    else if (stat.isFile() && !stat.isSymbolicLink()) files.push({ path: `${packagePath}/${child}`, mode: stat.mode & 0o777, sha256: sha256(fs.readFileSync(absolute)) });
    else fail(`parser closure has a non-regular entry: ${packagePath}/${child}`);
  }
}
function packageFiles(packagePaths) {
  const files = [];
  for (const packagePath of packagePaths) {
    const root = path.join(nodeModules, packagePath.replace(/^node_modules\//u, ''));
    const rootStat = fs.lstatSync(root);
    if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) fail(`parser closure package is not a physical directory: ${packagePath}`);
    visitPackageFiles(packagePath, root, '', files);
  }
  return files.sort((left, right) => left.path.localeCompare(right.path));
}

const lockBytes = fs.readFileSync(packageLockPath);
const lock = readJSON(packageLockPath);
if (lock.lockfileVersion !== 3 || !lock.packages) fail('package-lock.json must use lockfileVersion 3');
const nodeModules = process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED || path.join(appRoot, 'node_modules');
const packages = closurePackages();
const files = packageFiles(packages);
const document = {
  schemaVersion: 2,
  generator: 'frontend-app/scripts/lib/generate-frontend-code-size-dependency-closure.mjs',
  generatorSha256: sha256(generatorBytes),
  packageLockSha256: sha256(lockBytes),
  rootPackage: '@babel/parser',
  packages,
  files,
};
document.closureSha256 = sha256(JSON.stringify(document));
if (process.argv.slice(2).join(' ') === '--check') {
  const expected = fs.readFileSync(outputPath, 'utf8');
  const actual = `${JSON.stringify(document, null, 2)}\n`;
  if (expected !== actual) fail(`frontend code-size dependency closure drift: ${outputPath}`);
  process.stdout.write(`FRONTEND_CODE_SIZE_CLOSURE\t${document.closureSha256}\n`);
} else if (process.argv.length === 2) {
  fs.writeFileSync(outputPath, `${JSON.stringify(document, null, 2)}\n`, { mode: 0o644 });
  process.stdout.write(`FRONTEND_CODE_SIZE_CLOSURE\t${document.closureSha256}\n`);
} else {
  fail('usage: node scripts/lib/generate-frontend-code-size-dependency-closure.mjs [--check]');
}

import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIRECTORY = dirname(fileURLToPath(import.meta.url));
const DEFAULT_BASELINE_PATH = resolve(SCRIPT_DIRECTORY, 'frontend-maintainability-baseline.json');

export {
  DEFAULT_BASELINE_PATH,
};

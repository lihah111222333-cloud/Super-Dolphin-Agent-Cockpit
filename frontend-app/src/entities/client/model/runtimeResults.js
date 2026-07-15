import { createRuntimeResultHelperSet } from './helpers/runtimeResultHelpers.js';

export { RUNTIME_TOOL_TERMINAL_STATUSES, ContractError } from './helpers/runtimeResultHelpers.js';

export function createRuntimeResultHelpers(deps = {}) {
  return createRuntimeResultHelperSet(deps);
}

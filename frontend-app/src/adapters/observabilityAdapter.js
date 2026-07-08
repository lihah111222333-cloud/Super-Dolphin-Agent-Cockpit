import { parseObservabilityResultResponse } from '../shared/api/backendSchemas.js';

function adaptObservabilityResult(response) {
  return parseObservabilityResultResponse(response);
}

export { adaptObservabilityResult };

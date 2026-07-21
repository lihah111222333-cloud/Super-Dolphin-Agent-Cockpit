import { firstPresentText } from '../../pages/shared/pageShared.js';

export function updateVersionFromResult(result) {
  return firstPresentText(result?.version, result?.artifact?.version);
}

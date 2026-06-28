import {
  EVENT_COMPAT_WIRE_PREFIXES,
  EVENT_RAW_WIRE_METHODS,
  EVENT_RAW_WIRE_PREFIXES,
  EVENT_RAW_WIRE_SUFFIXES,
  EVENT_TYPED_WIRE_METHODS,
} from './eventWireMethods.js';

const typedWireMethodSet = new Set(EVENT_TYPED_WIRE_METHODS);
const rawWireMethodSet = new Set(EVENT_RAW_WIRE_METHODS);

export function isKnownEventWireMethod(method) {
  if (!method || typeof method !== 'string') return false;
  if (typedWireMethodSet.has(method) || rawWireMethodSet.has(method)) return true;
  return EVENT_RAW_WIRE_PREFIXES.some((prefix) => method.startsWith(prefix)) ||
    EVENT_COMPAT_WIRE_PREFIXES.some((prefix) => method.startsWith(prefix)) ||
    EVENT_RAW_WIRE_SUFFIXES.some((suffix) => method.endsWith(suffix));
}

export function asEventWireNotification(method, payload) {
  if (!method || typeof method !== 'string') {
    throw new Error('event wire method is required');
  }
  if (!isKnownEventWireMethod(method)) {
    throw new Error(`unknown event wire method: ${method}`);
  }
  return { method, payload };
}

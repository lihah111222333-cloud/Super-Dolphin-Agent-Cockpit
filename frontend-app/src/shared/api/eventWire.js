import {
  EVENT_BRIDGE_CONTROL_WIRE_METHODS,
  EVENT_BRIDGE_CONTROL_WIRE_SUFFIXES,
  EVENT_COMPAT_WIRE_PREFIXES,
  EVENT_RAW_WIRE_METHODS,
  EVENT_RAW_WIRE_PREFIXES,
  EVENT_RAW_WIRE_SUFFIXES,
  EVENT_TYPED_WIRE_METHODS,
} from './eventWireMethods.js';

const typedWireMethodSet = new Set(EVENT_TYPED_WIRE_METHODS);
const rawWireMethodSet = new Set(EVENT_RAW_WIRE_METHODS);
const bridgeControlWireMethodSet = new Set(EVENT_BRIDGE_CONTROL_WIRE_METHODS);

function eventWireMethod(input) {
  if (input && typeof input === 'object' && !Array.isArray(input)) {
    return input.method || input.type;
  }
  return input;
}

function eventWirePayload(input, fallbackPayload) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) return fallbackPayload;
  if (Object.prototype.hasOwnProperty.call(input, 'payload')) return input.payload;
  if (Object.prototype.hasOwnProperty.call(input, 'params')) return input.params;
  if (Object.prototype.hasOwnProperty.call(input, 'data')) return input.data;
  return {};
}

export function isKnownEventWireMethod(method) {
  if (!method || typeof method !== 'string') return false;
  const bridgeControlMethod = method.toLowerCase();
  if (
    typedWireMethodSet.has(method) ||
    rawWireMethodSet.has(method) ||
    bridgeControlWireMethodSet.has(bridgeControlMethod)
  ) {
    return true;
  }
  return EVENT_RAW_WIRE_PREFIXES.some((prefix) => method.startsWith(prefix)) ||
    EVENT_COMPAT_WIRE_PREFIXES.some((prefix) => method.startsWith(prefix)) ||
    EVENT_RAW_WIRE_SUFFIXES.some((suffix) => method.endsWith(suffix)) ||
    EVENT_BRIDGE_CONTROL_WIRE_SUFFIXES.some((suffix) => bridgeControlMethod.endsWith(suffix));
}

export function asEventWireNotification(method, payload) {
  const wireMethod = eventWireMethod(method);
  if (!wireMethod || typeof wireMethod !== 'string') {
    throw new Error('event wire method is required');
  }
  if (!isKnownEventWireMethod(wireMethod)) {
    throw new Error(`unknown event wire method: ${wireMethod}`);
  }
  return { method: wireMethod, payload: eventWirePayload(method, payload) };
}

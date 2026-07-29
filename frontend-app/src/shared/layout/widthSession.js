const WIDTH_SESSION_KINDS = Object.freeze(['rail', 'right']);

function requiredFinite(value, name) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) throw new TypeError(`${name} must be a finite number`);
  return numeric;
}

function requiredMetrics(input) {
  if (input === null || typeof input !== 'object') throw new TypeError('width metrics are required');
  const min = requiredFinite(input.min, 'width metrics min');
  const max = requiredFinite(input.max, 'width metrics max');
  if (max < min) throw new RangeError('width metrics max must be greater than or equal to min');
  return Object.freeze({ max, min });
}

function clamp(value, metrics) {
  return Math.max(metrics.min, Math.min(metrics.max, value));
}

function requiredCallback(value, name) {
  if (typeof value !== 'function') throw new TypeError(`${name} must be a function`);
  return value;
}

class WidthSession {
  #commitTargets;
  #preview;
  #state = null;

  constructor({ commitTargets, preview }) {
    if (commitTargets === null || typeof commitTargets !== 'object') {
      throw new TypeError('WidthSession commitTargets are required');
    }
    this.#commitTargets = Object.freeze({
      rail: requiredCallback(commitTargets.rail, 'WidthSession rail commit target'),
      right: requiredCallback(commitTargets.right, 'WidthSession right commit target'),
    });
    this.#preview = requiredCallback(preview, 'WidthSession preview');
  }

  get active() {
    return this.#state !== null;
  }

  begin(owner, input) {
    if (this.#state !== null) return false;
    if (owner === null || (typeof owner !== 'object' && typeof owner !== 'function')) {
      throw new TypeError('WidthSession owner is required');
    }
    if (!WIDTH_SESSION_KINDS.includes(input?.kind)) {
      throw new TypeError('WidthSession kind must be rail or right');
    }
    const metrics = requiredMetrics(input);
    const value = clamp(requiredFinite(input.value, 'WidthSession value'), metrics);
    this.#state = {
      currentValue: value,
      kind: input.kind,
      metrics,
      moved: false,
      owner,
      pointerId: requiredFinite(input.pointerId, 'WidthSession pointerId'),
      startCoordinate: requiredFinite(input.coordinate, 'WidthSession coordinate'),
      startValue: value,
    };
    return true;
  }

  move(owner, input) {
    if (!this.#owns(owner, input?.pointerId)) return false;
    if (input.min !== undefined || input.max !== undefined) {
      this.#state.metrics = requiredMetrics(input);
    }
    const coordinate = requiredFinite(input.coordinate, 'WidthSession coordinate');
    const delta = coordinate - this.#state.startCoordinate;
    const direction = this.#state.kind === 'right' ? -1 : 1;
    const nextValue = clamp(this.#state.startValue + (delta * direction), this.#state.metrics);
    this.#state.currentValue = nextValue;
    this.#state.moved = true;
    this.#preview(nextValue, this.#meta('move'));
    return true;
  }

  commit(owner, input) {
    if (!this.#owns(owner, input?.pointerId)) return false;
    const state = this.#state;
    this.#state = null;
    if (state.moved) this.#commitTargets[state.kind](state.currentValue);
    return true;
  }

  cancel(owner, input) {
    if (!this.#owns(owner, input?.pointerId, input?.reason !== 'pointercancel')) return false;
    const state = this.#state;
    const meta = Object.freeze({ kind: state.kind, phase: 'cancel', reason: input?.reason });
    this.#state = null;
    this.#preview(state.startValue, meta);
    return true;
  }

  migrateViewport(input) {
    if (this.#state === null) return false;
    const metrics = requiredMetrics(input);
    this.#state.metrics = metrics;
    const nextValue = clamp(this.#state.currentValue, metrics);
    if (nextValue !== this.#state.currentValue) {
      this.#state.currentValue = nextValue;
      this.#preview(nextValue, this.#meta('migrateViewport'));
    }
    return true;
  }

  #meta(phase) {
    return Object.freeze({ kind: this.#state.kind, phase });
  }

  #owns(owner, pointerId, allowMissingPointer = false) {
    if (this.#state === null || this.#state.owner !== owner) return false;
    if (allowMissingPointer && pointerId === undefined) return true;
    return Number(pointerId) === this.#state.pointerId;
  }
}

export { WIDTH_SESSION_KINDS, WidthSession };

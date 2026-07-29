function requiredFinite(value, name) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) throw new TypeError(`${name} must be a finite number`);
  return numeric;
}

function requiredMetrics(input) {
  if (input === null || typeof input !== 'object') throw new TypeError('activity height metrics are required');
  const min = requiredFinite(input.min, 'activity height metrics min');
  const max = requiredFinite(input.max, 'activity height metrics max');
  if (max < min) throw new RangeError('activity height metrics max must be greater than or equal to min');
  return Object.freeze({ max, min });
}

function requiredCallback(value, name) {
  if (typeof value !== 'function') throw new TypeError(`${name} must be a function`);
  return value;
}

function clamp(value, metrics) {
  return Math.max(metrics.min, Math.min(metrics.max, value));
}

class ActivityHeightSession {
  #commit;
  #preview;
  #state = null;

  constructor({ commit, preview }) {
    this.#commit = requiredCallback(commit, 'ActivityHeightSession commit target');
    this.#preview = requiredCallback(preview, 'ActivityHeightSession preview');
  }

  get active() {
    return this.#state !== null;
  }

  begin(owner, input) {
    if (this.#state !== null) return false;
    if (owner === null || (typeof owner !== 'object' && typeof owner !== 'function')) {
      throw new TypeError('ActivityHeightSession owner is required');
    }
    const metrics = requiredMetrics(input);
    const value = clamp(requiredFinite(input.value, 'ActivityHeightSession value'), metrics);
    this.#state = {
      currentValue: value,
      metrics,
      moved: false,
      owner,
      pointerId: requiredFinite(input.pointerId, 'ActivityHeightSession pointerId'),
      startCoordinate: requiredFinite(input.coordinate, 'ActivityHeightSession coordinate'),
      startValue: value,
    };
    return true;
  }

  move(owner, input) {
    if (!this.#owns(owner, input?.pointerId)) return false;
    if (input.min !== undefined || input.max !== undefined) {
      this.#state.metrics = requiredMetrics(input);
    }
    const coordinate = requiredFinite(input.coordinate, 'ActivityHeightSession coordinate');
    const nextValue = clamp(
      this.#state.startValue + (this.#state.startCoordinate - coordinate),
      this.#state.metrics,
    );
    this.#state.currentValue = nextValue;
    this.#state.moved = true;
    this.#preview(nextValue, Object.freeze({ phase: 'move' }));
    return true;
  }

  commit(owner, input) {
    if (!this.#owns(owner, input?.pointerId)) return false;
    const state = this.#state;
    this.#state = null;
    if (state.moved) this.#commit(state.currentValue);
    return true;
  }

  cancel(owner, input) {
    if (!this.#owns(owner, input?.pointerId, input?.reason !== 'pointercancel')) return false;
    const state = this.#state;
    this.#state = null;
    this.#preview(state.startValue, Object.freeze({ phase: 'cancel', reason: input?.reason }));
    return true;
  }

  migrateViewport(input) {
    if (this.#state === null) return false;
    const metrics = requiredMetrics(input);
    this.#state.metrics = metrics;
    const nextValue = clamp(this.#state.currentValue, metrics);
    if (nextValue !== this.#state.currentValue) {
      this.#state.currentValue = nextValue;
      this.#preview(nextValue, Object.freeze({ phase: 'migrateViewport' }));
    }
    return true;
  }

  #owns(owner, pointerId, allowMissingPointer = false) {
    if (this.#state === null || this.#state.owner !== owner) return false;
    if (allowMissingPointer && pointerId === undefined) return true;
    return Number(pointerId) === this.#state.pointerId;
  }
}

export { ActivityHeightSession };

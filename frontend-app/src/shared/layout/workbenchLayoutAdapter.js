import { ActivityHeightSession } from './activityHeightSession.js';
import { WidthSession } from './widthSession.js';

const RESIZER_KEY_STEP = 16;

function keyboardValue(event, current, min, max, direction = 1) {
  if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return null;
  if (event.key === 'Home') return min;
  if (event.key === 'End') return max;
  if (event.key === 'ArrowLeft') return Math.max(min, Math.min(max, current - (RESIZER_KEY_STEP * direction)));
  if (event.key === 'ArrowRight') return Math.max(min, Math.min(max, current + (RESIZER_KEY_STEP * direction)));
  return null;
}

function activityKeyboardValue(event, current, min, max) {
  if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return null;
  if (event.key === 'Home') return min;
  if (event.key === 'End') return max;
  if (event.key === 'ArrowUp' || event.key === 'PageUp') return Math.min(max, current + RESIZER_KEY_STEP);
  if (event.key === 'ArrowDown' || event.key === 'PageDown') return Math.max(min, current - RESIZER_KEY_STEP);
  return null;
}

function requiredPointer(event, label) {
  const pointerId = Number(event.pointerId);
  if (!Number.isFinite(pointerId)) throw new TypeError(`${label} pointerId must be finite`);
  return pointerId;
}

class WorkbenchLayoutAdapter {
  constructor(callbacks) {
    this.callbacks = callbacks;
    this.geometry = null;
    this.widthActive = null;
    this.activityActive = null;
    this.widthSession = new WidthSession({
      commitTargets: {
        rail: (value) => this.callbacks.setRailWidth(value),
        right: (value) => this.commitRight(value),
      },
      preview: (value, meta) => this.previewWidth(value, meta),
    });
    this.activitySession = new ActivityHeightSession({
      commit: (value) => this.callbacks.setActivityHeight(value),
      preview: (value) => this.callbacks.setActivityHeight(value),
    });
    this.actions = Object.freeze({
      activity: Object.freeze({
        begin: (event) => this.beginActivity(event),
        keyDown: (event) => this.activityKeyDown(event),
      }),
      rail: Object.freeze({
        begin: (event) => this.beginWidth('rail', event),
        keyDown: (event) => this.railKeyDown(event),
      }),
      right: Object.freeze({
        begin: (event) => this.beginWidth('right', event),
        keyDown: (event) => this.rightKeyDown(event),
        setOpen: (next) => this.setRightOpen(next),
      }),
    });
  }

  update(callbacks, geometry) {
    this.callbacks = callbacks;
    this.geometry = geometry;
    this.migrateViewport();
  }

  previewWidth(value, meta) {
    if (meta.kind === 'rail') this.callbacks.setRailWidth(value);
    else this.callbacks.setRightPreview(meta.phase === 'cancel' ? null : value);
  }

  commitRight(value) {
    this.callbacks.setRightPreview(null);
    this.callbacks.setRightPreference(value);
    if (value === 0) this.callbacks.setRightOpen(false);
  }

  beginWidth(kind, event) {
    event.preventDefault();
    const geometry = this.requiredGeometry();
    const owner = event.currentTarget;
    const pointerId = requiredPointer(event, 'WidthSession');
    const area = kind === 'rail' ? geometry.rail : geometry.right;
    const min = kind === 'rail' ? area.min : 0;
    if (!this.widthSession.begin(owner, {
      coordinate: event.clientX,
      kind,
      max: area.max,
      min,
      pointerId,
      value: area.displayed,
    })) return;
    owner.setPointerCapture?.(pointerId);
    const listeners = this.widthListeners(kind, owner);
    this.widthActive = { kind, listeners, owner, pointerId };
    listeners.add();
  }

  widthListeners(kind, owner) {
    const move = (event) => {
      const area = kind === 'rail' ? this.requiredGeometry().rail : this.requiredGeometry().right;
      this.widthSession.move(owner, {
        coordinate: event.clientX,
        max: area.max,
        min: kind === 'rail' ? area.min : 0,
        pointerId: event.pointerId,
      });
    };
    const up = (event) => {
      if (this.widthSession.commit(owner, { pointerId: event.pointerId })) this.cleanupWidth();
    };
    const pointerCancel = (event) => {
      if (this.widthSession.cancel(owner, { pointerId: event.pointerId, reason: 'pointercancel' })) this.cleanupWidth();
    };
    const blur = () => {
      if (this.widthSession.cancel(owner, { reason: 'blur' })) this.cleanupWidth();
    };
    return this.listenerSet({ blur, move, pointerCancel, up });
  }

  beginActivity(event) {
    event.preventDefault();
    const geometry = this.requiredGeometry();
    const owner = event.currentTarget;
    const pointerId = requiredPointer(event, 'ActivityHeightSession');
    if (!this.activitySession.begin(owner, {
      coordinate: event.clientY,
      max: geometry.activity.max,
      min: geometry.activity.min,
      pointerId,
      value: geometry.activity.displayed,
    })) return;
    owner.setPointerCapture?.(pointerId);
    const listeners = this.activityListeners(owner);
    this.activityActive = { listeners, owner, pointerId };
    listeners.add();
  }

  activityListeners(owner) {
    const move = (event) => {
      const activity = this.requiredGeometry().activity;
      this.activitySession.move(owner, {
        coordinate: event.clientY,
        max: activity.max,
        min: activity.min,
        pointerId: event.pointerId,
      });
    };
    const up = (event) => {
      if (this.activitySession.commit(owner, { pointerId: event.pointerId })) this.cleanupActivity();
    };
    const pointerCancel = (event) => {
      if (this.activitySession.cancel(owner, { pointerId: event.pointerId, reason: 'pointercancel' })) this.cleanupActivity();
    };
    const blur = () => {
      if (this.activitySession.cancel(owner, { reason: 'blur' })) this.cleanupActivity();
    };
    return this.listenerSet({ blur, move, pointerCancel, up });
  }

  listenerSet({ blur, move, pointerCancel, up }) {
    return {
      add: () => {
        window.addEventListener('pointermove', move);
        window.addEventListener('pointerup', up);
        window.addEventListener('pointercancel', pointerCancel);
        window.addEventListener('blur', blur);
      },
      remove: () => {
        window.removeEventListener('pointermove', move);
        window.removeEventListener('pointerup', up);
        window.removeEventListener('pointercancel', pointerCancel);
        window.removeEventListener('blur', blur);
      },
    };
  }

  cancelWidth(reason, kind) {
    const active = this.widthActive;
    if (!active || (kind && active.kind !== kind)) return false;
    if (!this.widthSession.cancel(active.owner, { reason })) return false;
    this.cleanupWidth();
    return true;
  }

  cancelActivity(reason) {
    const active = this.activityActive;
    if (!active || !this.activitySession.cancel(active.owner, { reason })) return false;
    this.cleanupActivity();
    return true;
  }

  cleanupWidth() {
    const active = this.widthActive;
    if (!active) return;
    active.listeners.remove();
    active.owner.releasePointerCapture?.(active.pointerId);
    this.widthActive = null;
  }

  cleanupActivity() {
    const active = this.activityActive;
    if (!active) return;
    active.listeners.remove();
    active.owner.releasePointerCapture?.(active.pointerId);
    this.activityActive = null;
  }

  migrateViewport() {
    if (!this.geometry) return;
    if (this.widthActive) {
      const area = this.widthActive.kind === 'rail' ? this.geometry.rail : this.geometry.right;
      this.widthSession.migrateViewport({
        max: area.max,
        min: this.widthActive.kind === 'rail' ? area.min : 0,
      });
    }
    this.activitySession.migrateViewport({
      max: this.geometry.activity.max,
      min: this.geometry.activity.min,
    });
  }

  activityKeyDown(event) {
    const activity = this.requiredGeometry().activity;
    const value = activityKeyboardValue(event, activity.displayed, activity.min, activity.max);
    if (value === null) return;
    event.preventDefault();
    this.callbacks.setActivityHeight(value);
  }

  railKeyDown(event) {
    const rail = this.requiredGeometry().rail;
    const value = keyboardValue(event, rail.displayed, rail.min, rail.max);
    if (value === null) return;
    event.preventDefault();
    this.callbacks.setRailWidth(value);
  }

  rightKeyDown(event) {
    const right = this.requiredGeometry().right;
    const value = keyboardValue(event, right.displayed, 0, right.max, -1);
    if (value === null) return;
    event.preventDefault();
    this.callbacks.setRightPreference(value);
    if (value === 0) this.callbacks.setRightOpen(false);
  }

  setRightOpen(next) {
    const right = this.requiredGeometry().right;
    const open = typeof next === 'function' ? next(right.open) : next;
    if (typeof open !== 'boolean') throw new TypeError('right panel open state must be boolean');
    if (open && right.preference === 0) this.callbacks.setRightPreference(right.defaultWidth);
    this.callbacks.setRightOpen(open);
  }

  dispose() {
    this.cancelWidth('unmount');
    this.cancelActivity('unmount');
  }

  requiredGeometry() {
    if (!this.geometry) throw new Error('WorkbenchLayoutAdapter geometry is not ready');
    return this.geometry;
  }
}

export { WorkbenchLayoutAdapter };

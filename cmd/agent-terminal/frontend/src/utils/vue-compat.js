import React, { useState, useEffect, useMemo, useRef, useCallback } from 'react';
import { reactive, watch, isRef, isReactive, effectScope } from '../../lib/vue.esm-browser.prod.js';

const SYNC_TRIGGER_LIMIT_PER_SECOND = 100;

export function val(x) {
  if (x && typeof x === 'object' && 'value' in x) {
    return x.value;
  }
  return x;
}

function isDomEl(x) {
  return !!(x && (
    (typeof Element !== 'undefined' && x instanceof Element) ||
    typeof x.nodeType === 'number'
  ));
}

function disposeVm(vm, meta, setupMeta) {
  if (!meta) return;
  meta.disposed = true;
  meta.scope.stop();
  meta.beforeUnmountCbs.forEach((cb) => cb());
  setupMeta.current.delete(vm);
  if (globalThis.__ACTIVE_VMS__) {
    globalThis.__ACTIVE_VMS__ = globalThis.__ACTIVE_VMS__.filter((v) => v !== vm);
  }
}

function scheduleVmCleanup(vm, meta, setupMeta) {
  if (!meta) return;
  meta.committed = false;
  const isTestEnv = typeof process !== 'undefined' && process.env.NODE_ENV === 'test';
  if (isTestEnv) {
    disposeVm(vm, meta, setupMeta);
  } else {
    meta.cleanupTimeoutId = setTimeout(() => {
      if (!meta.committed && !meta.disposed) {
        disposeVm(vm, meta, setupMeta);
      }
    }, 100);
  }
}

export function useVueSetup(setupFn, props, emit) {
  const [, setTick] = useState(0);
  const emitRef = useRef(emit);
  const setupMeta = useRef(new WeakMap());
  const vmRef = useRef(null);
  const lastCreatedVmRef = useRef(null);
  const loopCounter = useRef({ count: 0, lastTime: Date.now(), keys: new Map(), throttled: false });
  const isRenderingRef = useRef(false);
  const createdVmsRef = useRef(new Set());

  emitRef.current = emit;

  const stableEmit = useCallback((...args) => {
    if (typeof emitRef.current === 'function') {
      return emitRef.current(...args);
    }
    return undefined;
  }, []);

  // Create a reactive props object for Vue
  const vueProps = useMemo(() => {
    return reactive({ ...props });
  }, []);

  // Synchronize React props to Vue reactive props immediately during the render phase
  isRenderingRef.current = true;
  for (const key of Object.keys(vueProps)) {
    if (!(key in props)) {
      delete vueProps[key];
    }
  }
  for (const key of Object.keys(props)) {
    if (vueProps[key] !== props[key]) {
      vueProps[key] = props[key];
    }
  }
  isRenderingRef.current = false;

  // Instantiate the VM inside useMemo
  const vm = useMemo(() => {
    const beforeUnmountCbs = [];
    const mountedCbs = [];
    const scope = effectScope(true);
    const hasWindow = typeof window !== 'undefined';
    const previousWindowValues = hasWindow ? new Map([
      ['__VUE_SETUP_ACTIVE__', window.__VUE_SETUP_ACTIVE__],
      ['__VUE_COMPAT_MOUNTED_HOOKS__', window.__VUE_COMPAT_MOUNTED_HOOKS__],
      ['__VUE_COMPAT_UNMOUNT_HOOKS__', window.__VUE_COMPAT_UNMOUNT_HOOKS__],
      ['__VUE_ON_BEFORE_UNMOUNT__', window.__VUE_ON_BEFORE_UNMOUNT__],
      ['__VUE_ON_UNMOUNTED__', window.__VUE_ON_UNMOUNTED__],
      ['__VUE_ON_MOUNTED__', window.__VUE_ON_MOUNTED__],
    ]) : new Map();

    const restoreWindowHooks = () => {
      if (!hasWindow) return;
      previousWindowValues.forEach((value, key) => {
        if (value === undefined) {
          delete window[key];
        } else {
          window[key] = value;
        }
      });
    };

    if (hasWindow) {
      window.__VUE_SETUP_ACTIVE__ = true;
      window.__VUE_COMPAT_MOUNTED_HOOKS__ = mountedCbs;
      window.__VUE_COMPAT_UNMOUNT_HOOKS__ = beforeUnmountCbs;
      window.__VUE_ON_BEFORE_UNMOUNT__ = (cb) => beforeUnmountCbs.push(cb);
      window.__VUE_ON_UNMOUNTED__ = (cb) => beforeUnmountCbs.push(cb);
      window.__VUE_ON_MOUNTED__ = (cb) => mountedCbs.push(cb);
    }

    try {
      const res = scope.run(() => setupFn(vueProps, { emit: stableEmit })) || {};
      const normalized = res && typeof res === 'object' ? res : {};
      normalized.__uid = Math.random().toString(36).slice(2, 6);
      
      globalThis.__ACTIVE_VMS__ = globalThis.__ACTIVE_VMS__ || [];
      globalThis.__ACTIVE_VMS__.push(normalized);
      
      createdVmsRef.current.add(normalized);
      
      const meta = { beforeUnmountCbs, mountedCbs, scope, committed: false, disposed: false, cleanupTimeoutId: null };
      setupMeta.current.set(normalized, meta);

      // Fallback cleanup if the VM is never committed within 100ms
      meta.cleanupTimeoutId = setTimeout(() => {
        if (!meta.committed && !meta.disposed) {
          disposeVm(normalized, meta, setupMeta);
        }
      }, 100);

      lastCreatedVmRef.current = normalized;
      vmRef.current = normalized;
      return normalized;
    } catch (error) {
      scope.stop();
      throw error;
    } finally {
      restoreWindowHooks();
    }
  }, [vueProps]);

  // Run onMounted hooks on React mount
  useEffect(() => {
    const meta = setupMeta.current.get(vm);
    if (meta) {
      meta.committed = true;
      if (meta.cleanupTimeoutId) {
        clearTimeout(meta.cleanupTimeoutId);
        meta.cleanupTimeoutId = null;
      }
    }

    // Immediately clean up/dispose of all other uncommitted VMs created by this hook
    if (createdVmsRef.current) {
      for (const otherVm of createdVmsRef.current) {
        if (otherVm !== vm) {
          const otherMeta = setupMeta.current.get(otherVm);
          if (otherMeta && !otherMeta.disposed) {
            disposeVm(otherVm, otherMeta, setupMeta);
          }
          createdVmsRef.current.delete(otherVm);
        }
      }
    }

    meta?.mountedCbs.forEach((cb) => cb());
    if (meta?.mountedCbs.length) {
      setTick((t) => t + 1);
    }
  }, [vm]);

  // Watch all returned refs, computed values, and reactive objects.
  useEffect(() => {
    const watchStops = [];
    let disposed = false;
    let queuedTick = false;
    let rafId = 0;
    let timeoutId = 0;

    const flushQueuedTick = () => {
      queuedTick = false;
      rafId = 0;
      timeoutId = 0;
      if (!disposed) {
        setTick((t) => t + 1);
      }
    };

    const scheduleQueuedTick = () => {
      if (queuedTick) return;
      queuedTick = true;
      if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
        rafId = window.requestAnimationFrame(flushQueuedTick);
        return;
      }
      timeoutId = setTimeout(flushQueuedTick, 16);
    };

    const handleTrigger = (key) => {
      if (isRenderingRef.current) {
        return;
      }
      const now = Date.now();
      const counter = loopCounter.current;
      if (now - counter.lastTime > 1000) {
        counter.count = 0;
        counter.lastTime = now;
        counter.keys.clear();
        counter.throttled = false;
      }
      counter.count += 1;
      counter.keys.set(key, (counter.keys.get(key) || 0) + 1);

      if (counter.count > SYNC_TRIGGER_LIMIT_PER_SECOND) {
        counter.throttled = true;
      }
      if (counter.throttled) {
        scheduleQueuedTick();
        return;
      }
      setTick((t) => t + 1);
    };

    Object.keys(vm).forEach((key) => {
      const value = vm[key];
      if (isRef(value) || (value && typeof value === 'object' && 'value' in value)) {
        const isDom = key.toLowerCase().endsWith('ref') ||
          key.toLowerCase().includes('anchor') ||
          key.toLowerCase().includes('element') ||
          isDomEl(value) ||
          isDomEl(value.value);
        const stopWatch = watch(
          () => value.value,
          () => {
            handleTrigger(key);
          },
          { deep: !isDom, flush: 'sync' }
        );
        watchStops.push(stopWatch);
        return;
      }
      if (isReactive(value)) {
        const isDom = key.toLowerCase().endsWith('ref') ||
          key.toLowerCase().includes('anchor') ||
          key.toLowerCase().includes('element') ||
          isDomEl(value);
        const stopWatch = watch(
          value,
          () => {
            handleTrigger(key);
          },
          { deep: !isDom, flush: 'sync' }
        );
        watchStops.push(stopWatch);
      }
    });

    return () => {
      const meta = setupMeta.current.get(vm);
      disposed = true;
      if (rafId && typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
        window.cancelAnimationFrame(rafId);
      }
      if (timeoutId) {
        clearTimeout(timeoutId);
      }
      watchStops.forEach((stop) => stop());
      scheduleVmCleanup(vm, meta, setupMeta);
    };
  }, [vm]);

  return vm;
}

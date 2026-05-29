import * as Vue from './vue.esm-browser.prod.original.js';

function registerCompatHook(arrayKey, legacyFunctionKey, cb) {
  if (typeof window === 'undefined') return false;
  const bucket = window[arrayKey];
  if (Array.isArray(bucket)) {
    bucket.push(cb);
    return true;
  }
  const legacyRegister = window[legacyFunctionKey];
  if (typeof legacyRegister === 'function') {
    legacyRegister(cb);
    return true;
  }
  return false;
}

export const onMounted = (cb) => {
  if (registerCompatHook('__VUE_COMPAT_MOUNTED_HOOKS__', '__VUE_ON_MOUNTED__', cb)) {
    return;
  }
  Vue.onMounted(cb);
};

export const onBeforeUnmount = (cb) => {
  if (registerCompatHook('__VUE_COMPAT_UNMOUNT_HOOKS__', '__VUE_ON_BEFORE_UNMOUNT__', cb)) {
    return;
  }
  Vue.onBeforeUnmount(cb);
};

export const onUnmounted = (cb) => {
  if (registerCompatHook('__VUE_COMPAT_UNMOUNT_HOOKS__', '__VUE_ON_UNMOUNTED__', cb)) {
    return;
  }
  Vue.onUnmounted(cb);
};

// Re-export everything else from the original Vue bundle
export * from './vue.esm-browser.prod.original.js';

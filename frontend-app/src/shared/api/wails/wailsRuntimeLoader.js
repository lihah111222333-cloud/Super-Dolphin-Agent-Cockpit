// @ts-check
/// <reference path="./wailsBrowserRuntime.d.ts" />

/** @returns {Promise<unknown>} */
function loadWailsRuntime() {
  // public 目录里的 Wails runtime 只能由浏览器原生加载，避免 Vite 注入 ?import 后拦截。
  return import(/* @vite-ignore */ '/wails/runtime.js');
}

export { loadWailsRuntime };

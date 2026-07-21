/**
 * Browser-global Wails modules are injected by the desktop host and have no
 * filesystem-backed source module for Vite or TypeScript to resolve.
 */
declare module "*/wails/runtime.js" {
  const browserInjectedWailsModule: unknown;
  export default browserInjectedWailsModule;
}

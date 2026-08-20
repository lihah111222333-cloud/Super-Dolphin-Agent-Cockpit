declare module '*.png' {
  const src: string;
  export default src;
}

declare module 'react' {
  export type SetStateAction<T> = T | ((previous: T) => T);
  export type Dispatch<T> = (value: T) => void;
  export function useState<T>(initialState: T): [T, Dispatch<SetStateAction<T>>];

  const React: {
    StrictMode: any;
  };
  export default React;
}

declare module 'react/jsx-runtime' {
  export const Fragment: any;
  export const jsx: any;
  export const jsxs: any;
}

declare module 'react-dom/client' {
  export function createRoot(
    container: Element | DocumentFragment | null,
  ): any;

  const ReactDOM: {
    createRoot: typeof createRoot;
  };
  export default ReactDOM;
}

declare namespace JSX {
  interface Element {}
  interface IntrinsicElements {
    [elementName: string]: any;
  }
}

declare module '@testing-library/react' {
  export const render: any;
  export const screen: any;
}

declare module '@testing-library/user-event' {
  const userEvent: any;
  export default userEvent;
}

declare module 'vitest' {
  export const describe: any;
  export const expect: any;
  export const test: any;
}

declare module '@testing-library/jest-dom/vitest' {}

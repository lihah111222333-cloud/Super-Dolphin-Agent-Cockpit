import { watch } from '../../lib/vue.esm-browser.prod.js';

export function useDagStatusEventBridge(props, dagDetail) {
  watch(
    () => props.statusEvent,
    (event) => {
      if (!event) return;
      dagDetail.handleStatusEvent?.(event.payload || event);
    },
  );
}

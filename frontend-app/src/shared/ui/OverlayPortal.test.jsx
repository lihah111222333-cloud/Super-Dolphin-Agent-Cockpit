import React from 'react';
import { cleanup, render, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { OverlayPortal } from './OverlayPortal.jsx';
import { requiredOverlayRoot } from './overlayPortalRoot.js';

let caller;
let overlayHost;

beforeEach(() => {
  caller = document.createElement('div');
  caller.dataset.testid = 'portal-caller';
  const hosts = document.querySelectorAll('#overlay-root');
  overlayHost = hosts[0];
  if (hosts.length !== 1 || !(overlayHost instanceof HTMLElement)) {
    throw new Error('OverlayPortal tests require one overlay-root fixture.');
  }
  document.body.append(caller);
});

afterEach(() => {
  cleanup();
  caller.remove();
  document.querySelectorAll('#overlay-root').forEach((node) => node.remove());
});

describe('OverlayPortal', () => {
  it('uses the canonical required overlay host locator', () => {
    expect(requiredOverlayRoot()).toBe(overlayHost);
  });

  it('mounts children only in the unique overlay host and cleans them on unmount', () => {
    const view = render(
      <OverlayPortal>
        <div>Portaled content</div>
      </OverlayPortal>,
      { container: caller },
    );

    expect(within(overlayHost).getByText('Portaled content')).toBeInTheDocument();
    expect(within(caller).queryByText('Portaled content')).not.toBeInTheDocument();

    view.unmount();
    expect(within(overlayHost).queryByText('Portaled content')).not.toBeInTheDocument();
  });

  it('fails synchronously when the overlay host is missing instead of using body or inline fallback', () => {
    overlayHost.remove();

    expect(() => render(<OverlayPortal>Missing host</OverlayPortal>, { container: caller }))
      .toThrow(/overlay-root/);
    expect(document.body).not.toHaveTextContent('Missing host');
    expect(caller).not.toHaveTextContent('Missing host');
  });

  it('fails synchronously when duplicate overlay hosts exist', () => {
    const duplicate = document.createElement('div');
    duplicate.id = 'overlay-root';
    document.body.append(duplicate);

    expect(() => render(<OverlayPortal>Duplicate host</OverlayPortal>, { container: caller }))
      .toThrow(/overlay-root/);
    expect(overlayHost).not.toHaveTextContent('Duplicate host');
    expect(duplicate).not.toHaveTextContent('Duplicate host');
  });
});

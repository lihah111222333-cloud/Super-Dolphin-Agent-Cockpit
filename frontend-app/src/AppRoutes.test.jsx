import React from 'react';
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ActivePageContent } from './AppRoutes.jsx';
import { APP_COPY } from './shared/i18n/appI18n.js';

describe('ActivePageContent retired routes', () => {
  it('renders no page for the retired custom prompts route', () => {
    const { container } = render(
      <ActivePageContent
        activePage="prompts"
        copy={APP_COPY.zh}
        projectPath="/repo/app"
        store={{}}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

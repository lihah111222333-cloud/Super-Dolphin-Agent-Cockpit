import React, { useContext } from 'react';
import { MarkdownActionContext } from './json-render-markdown-action-key.jsx';
import { WIDGET_REGISTRY } from './JsonRenderWidgets.jsx';

export function JsonRenderer({ spec, markdownActionHandlers, onFileRefClick, onCitationClick }) {
  const inheritedHandlers = useContext(MarkdownActionContext);
  const handlers = markdownActionHandlers || inheritedHandlers;

  if (!spec || typeof spec !== 'object') {
    return <span className="jr-empty">(empty)</span>;
  }

  const typeName = (spec.type || '').toString().trim();
  const entry = WIDGET_REGISTRY[typeName];

  if (!entry) {
    return (
      <div className="jr-unknown">
        <span className="jr-unknown-type">Unknown: {typeName || '(no type)'}</span>
      </div>
    );
  }

  const Component = entry.component;

  return (
    <MarkdownActionContext.Provider value={handlers}>
      <Component
        spec={spec}
        onFileRefClick={onFileRefClick}
        onCitationClick={onCitationClick}
      />
    </MarkdownActionContext.Provider>
  );
}

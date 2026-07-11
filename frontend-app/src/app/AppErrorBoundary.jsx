import React, { Component } from 'react';
import { reportFrontendCrash } from '../shared/diagnostics/frontendCrashReport.js';

const SystemDate = globalThis.Date;

function currentTimestampISO() {
  return new SystemDate().toISOString();
}

function resolveRouteId(routeId) {
  return typeof routeId === 'function' ? routeId() : routeId;
}

function resolveBreadcrumbs(breadcrumbs) {
  if (breadcrumbs === undefined) return [];
  return typeof breadcrumbs.snapshot === 'function' ? breadcrumbs.snapshot() : breadcrumbs;
}

export class AppErrorBoundary extends Component {
  constructor(props) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError() {
    return { hasError: true };
  }

  componentDidCatch(error, errorInfo) {
    void reportFrontendCrash({
      input: {
        actionCode: 'app.render.crash',
        routeId: resolveRouteId(this.props.routeId),
        phase: 'render',
        timestamp: currentTimestampISO(),
        breadcrumbs: resolveBreadcrumbs(this.props.breadcrumbs),
        error,
        componentStack: errorInfo.componentStack,
      },
      reporter: this.props.reporter,
    });
  }

  retry = () => {
    this.setState({ hasError: false });
  };

  reload = () => {
    this.props.reload();
  };

  render() {
    if (!this.state.hasError) return this.props.children;
    return (
      <section role="alert" aria-labelledby="app-error-boundary-title">
        <h1 id="app-error-boundary-title">界面发生错误</h1>
        <p>当前界面无法继续显示。你可以重试界面，或重新加载应用。</p>
        <button type="button" onClick={this.retry}>重试界面</button>
        <button type="button" onClick={this.reload}>重新加载</button>
      </section>
    );
  }
}

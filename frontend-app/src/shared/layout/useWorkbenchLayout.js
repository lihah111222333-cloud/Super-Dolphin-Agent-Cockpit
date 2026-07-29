import { useEffect, useMemo, useState } from 'react';
import { solveWorkbenchGeometry } from './workbenchGeometry.js';
import { WorkbenchLayoutAdapter } from './workbenchLayoutAdapter.js';

const RAIL_DEFAULT_WIDTH = 340;
const ACTIVITY_DEFAULT_HEIGHT = 112;

function currentViewport() {
  if (typeof window === 'undefined') throw new Error('Workbench layout requires a browser viewport');
  const width = Number(window.innerWidth);
  const height = Number(window.innerHeight);
  if (!Number.isFinite(width) || !Number.isFinite(height)) {
    throw new TypeError('Workbench viewport dimensions must be finite');
  }
  return Object.freeze({ height, width });
}

function useViewport() {
  const [viewport, setViewport] = useState(currentViewport);
  useEffect(() => {
    let frameId = null;
    const resize = () => {
      if (frameId !== null) return;
      frameId = window.requestAnimationFrame(() => {
        frameId = null;
        setViewport(currentViewport());
      });
    };
    window.addEventListener('resize', resize);
    return () => {
      window.removeEventListener('resize', resize);
      if (frameId !== null) window.cancelAnimationFrame(frameId);
    };
  }, []);
  return viewport;
}

function useWorkbenchLayout({
  railOpen,
  rightOpen,
  rightPreference,
  setRightOpen,
  setRightPreference,
}) {
  const viewport = useViewport();
  const [railWidth, setRailWidth] = useState(RAIL_DEFAULT_WIDTH);
  const [rightPreview, setRightPreview] = useState(null);
  const [activityHeight, setActivityHeight] = useState(ACTIVITY_DEFAULT_HEIGHT);
  const geometry = useMemo(() => solveWorkbenchGeometry({
    activityHeight,
    railOpen,
    railWidth,
    rightDisplayWidth: rightPreview === null ? undefined : rightPreview,
    rightOpen,
    rightPreference,
    viewportHeight: viewport.height,
    viewportWidth: viewport.width,
  }), [activityHeight, railOpen, railWidth, rightOpen, rightPreference, rightPreview, viewport]);
  const [adapter] = useState(() => new WorkbenchLayoutAdapter({
    setActivityHeight,
    setRailWidth,
    setRightOpen,
    setRightPreference,
    setRightPreview,
  }));
  useEffect(() => {
    adapter.update({
      setActivityHeight,
      setRailWidth,
      setRightOpen,
      setRightPreference,
      setRightPreview,
    }, geometry);
  }, [adapter, geometry, setRightOpen, setRightPreference]);
  useEffect(() => {
    if (!rightOpen) adapter.cancelWidth('close', 'right');
  }, [adapter, rightOpen]);
  useEffect(() => {
    if (!railOpen) adapter.cancelWidth('close', 'rail');
  }, [adapter, railOpen]);
  useEffect(() => () => adapter.dispose(), [adapter]);
  return Object.freeze({ actions: adapter.actions, snapshot: geometry });
}

export { useWorkbenchLayout };

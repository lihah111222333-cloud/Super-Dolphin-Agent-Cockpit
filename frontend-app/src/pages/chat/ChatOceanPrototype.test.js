import { readFileSync } from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';

const oceanCss = readFileSync(path.join(cwd(), 'src/pages/chat/ChatOceanPrototype.css'), 'utf8');

describe('ChatOceanPrototype styles', () => {
  it('keeps the active conversation transparent so it cannot mask the atmosphere', () => {
    expect(oceanCss).toMatch(/\.chat-page--ocean \.conversation\s*\{[^}]*background:\s*transparent;[^}]*backdrop-filter:\s*none;/);
    expect(oceanCss).not.toContain('background: color-mix(in srgb, var(--bg) 88%, transparent)');
  });

  it('keeps decorative motion optional and the compact layout bounded', () => {
    expect(oceanCss).toContain('@media (prefers-reduced-motion: reduce)');
    expect(oceanCss).toMatch(/\.chat-ocean-orb,[\s\S]*\.chat-ocean-stars i,[\s\S]*\.chat-ocean-cloud,[\s\S]*\.chat-ocean-wave,[\s\S]*\.chat-ocean-glint[\s\S]*animation:\s*none/);
    expect(oceanCss).toContain('@media (max-width: 760px)');
    expect(oceanCss).toContain('pointer-events: none');
  });

  it('clips every contour to a wave and keeps fixed overlays out of ocean positioning rules', () => {
    expect(oceanCss).toMatch(/\.chat-ocean-wave\s*\{[^}]*overflow:\s*hidden;/);
    expect(oceanCss).not.toContain('.chat-page--ocean> :not(.chat-ocean-atmosphere)');
    expect(oceanCss).not.toMatch(/\.chat-page--ocean\s*>\s*:not\(\.chat-ocean-atmosphere\)/);
  });

  it('defines a faster and higher-energy active state with a reversible transition', () => {
    expect(oceanCss).toMatch(/\.chat-ocean-atmosphere\s*\{[^}]*transition:\s*background 1\.8s var\(--ocean-scene-ease\), filter 650ms ease;/);
    expect(oceanCss).toMatch(/\.chat-ocean-wave\s*\{[^}]*transition:[^;]*scale 650ms/);
    expect(oceanCss).toMatch(/\.chat-page--ocean-active \.chat-ocean-wave--near\s*\{[^}]*scale:\s*1\.02 1\.1;[^}]*animation-duration:\s*4\.2s;/);
  });

  it('recreates the coordinated day and night scene transition', () => {
    expect(oceanCss).toMatch(/\.chat-ocean-moon-shadow\s*\{[^}]*left:\s*112%;[^}]*transition:\s*left 1\.7s/);
    expect(oceanCss).toMatch(/data-theme="dark"\] \.chat-ocean-moon-shadow\s*\{[^}]*left:\s*66%/);
    expect(oceanCss).toMatch(/data-theme="dark"\] \.chat-ocean-stars\s*\{[^}]*opacity:\s*1/);
    expect(oceanCss).toMatch(/data-theme="dark"\] \.chat-ocean-cloud\s*\{[^}]*opacity:\s*0\.08/);
    expect(oceanCss).toMatch(/data-theme="dark"\] \.chat-ocean-light-path\s*\{[^}]*opacity:\s*0\.6/);
  });

  it('uses one exact 67 percent frosted-glass material for messages and composer', () => {
    expect(oceanCss).toContain('.message.user .chat-x-bubble .ant-bubble-content');
    expect(oceanCss).toContain('.message.assistant .chat-x-bubble .ant-bubble-content');
    expect(oceanCss).toContain('.composer .composer-card');
    expect(oceanCss).toMatch(/background:\s*rgb\(from var\(--surface\) r g b \/ 67%\)/);
    expect(oceanCss).toMatch(/--glass-card-background:\s*rgb\(from var\(--surface\) r g b \/ 67%\)/);
    expect(oceanCss).toMatch(/backdrop-filter:\s*blur\(18px\) saturate\(118%\)/);
  });
});

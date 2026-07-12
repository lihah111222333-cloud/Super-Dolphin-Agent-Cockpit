import { describe, expect, it, vi } from 'vitest'

import { defineAppCommandRegistry } from './appCommandRegistry'
import { createAppCommandRuntime } from './appCommandRuntime'

const registry = defineAppCommandRegistry([
  {
    id: 'chat.new',
    labelKey: 'commands.chat.new',
    section: 'chat',
    defaultShortcut: { key: 'n', mod: true },
  },
  {
    id: 'settings.open',
    labelKey: 'commands.settings.open',
    section: 'navigation',
    defaultShortcut: { key: ',', mod: true },
  },
])

function bindings(overrides = {}) {
  return {
    'chat.new': { run: vi.fn() },
    'settings.open': { run: vi.fn() },
    ...overrides,
  }
}

describe('createAppCommandRuntime', () => {
  it('projects registry descriptors into immutable executable commands', () => {
    const handlers = bindings()
    const runtime = createAppCommandRuntime({
      registry,
      bindings: handlers,
      platform: 'linux',
    })

    expect(runtime.commands).toHaveLength(2)
    expect(runtime.commands[0]).toMatchObject({
      id: 'chat.new',
      labelKey: 'commands.chat.new',
      section: 'chat',
      shortcut: { key: 'n', ctrl: true, meta: false, alt: false, shift: false },
    })
    expect(Object.isFrozen(runtime)).toBe(true)
    expect(Object.isFrozen(runtime.commands)).toBe(true)
    expect(Object.isFrozen(runtime.commands[0])).toBe(true)
    expect(runtime.commands[0]).not.toHaveProperty('defaultShortcut')
  })

  it.each([
    ['unknown command binding', { ...bindings(), 'unknown.command': { run: vi.fn() } }, {}, 'unknown command binding: unknown.command'],
    ['missing command handler', { 'chat.new': { run: vi.fn() } }, {}, 'missing command handler: settings.open'],
    ['extra binding field', bindings({ 'chat.new': { run: vi.fn(), id: 'shadow' } }), {}, 'unknown command binding field: chat.new.id'],
    ['non-function run', bindings({ 'chat.new': { run: true } }), {}, 'invalid command handler: chat.new.run'],
    ['non-function canExecute', bindings({ 'chat.new': { run: vi.fn(), canExecute: true } }), {}, 'invalid command handler: chat.new.canExecute'],
    ['non-string disabledReason', bindings({ 'chat.new': { run: vi.fn(), disabledReason: false } }), {}, 'invalid command disabled reason: chat.new'],
    ['unknown shortcut override', bindings(), { 'unknown.command': { key: 'x', ctrl: true } }, 'unknown shortcut override: unknown.command'],
  ])('fails fast for %s', (_name, commandBindings, overrides, message) => {
    expect(() => createAppCommandRuntime({
      registry,
      bindings: commandBindings,
      overrides,
      platform: 'linux',
    })).toThrow(message)
  })

  it('rejects effective shortcut conflicts after platform resolution', () => {
    expect(() => createAppCommandRuntime({
      registry,
      bindings: bindings(),
      overrides: {
        'settings.open': { key: 'n', ctrl: true },
      },
      platform: 'linux',
    })).toThrow('shortcut conflict: chat.new <-> settings.open')
  })

  it('executes known enabled commands and returns a structured result', () => {
    const run = vi.fn()
    const runtime = createAppCommandRuntime({
      registry,
      bindings: bindings({ 'chat.new': { run } }),
      platform: 'linux',
    })

    expect(runtime.execute('chat.new')).toEqual({ executed: true, reason: '' })
    expect(run).toHaveBeenCalledOnce()
  })

  it('does not execute disabled commands', () => {
    const run = vi.fn()
    const runtime = createAppCommandRuntime({
      registry,
      bindings: bindings({
        'chat.new': {
          run,
          canExecute: () => false,
          disabledReason: 'No active workspace',
        },
      }),
      platform: 'linux',
    })

    expect(runtime.execute('chat.new')).toEqual({
      executed: false,
      reason: 'No active workspace',
    })
    expect(run).not.toHaveBeenCalled()
  })

  it('fails fast when executing an unknown command', () => {
    const runtime = createAppCommandRuntime({
      registry,
      bindings: bindings(),
      platform: 'linux',
    })

    expect(() => runtime.execute('unknown.command')).toThrow('unknown command: unknown.command')
  })
})

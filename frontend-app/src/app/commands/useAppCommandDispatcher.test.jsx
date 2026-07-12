import React from 'react'
import { act, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { useAppCommandDispatcher } from './useAppCommandDispatcher'

function DispatcherHarness({ eventTarget, runtime }) {
  useAppCommandDispatcher({ eventTarget, runtime })
  return null
}

function createEventTarget() {
  let listener = null
  return {
    addEventListener: vi.fn((_type, nextListener) => {
      listener = nextListener
    }),
    removeEventListener: vi.fn((_type, oldListener) => {
      if (listener === oldListener) listener = null
    }),
    dispatch(event) {
      listener?.(event)
    },
  }
}

describe('useAppCommandDispatcher', () => {
  it('does not install a listener while the validated runtime is unavailable', () => {
    const eventTarget = createEventTarget()

    const view = render(<DispatcherHarness eventTarget={eventTarget} runtime={undefined} />)

    expect(eventTarget.addEventListener).not.toHaveBeenCalled()
    view.unmount()
    expect(eventTarget.removeEventListener).not.toHaveBeenCalled()
  })

  it('installs exactly one app-window keydown listener and removes the same listener', () => {
    const eventTarget = createEventTarget()
    const runtime = { commands: [], execute: vi.fn() }

    const view = render(<DispatcherHarness eventTarget={eventTarget} runtime={runtime} />)

    expect(eventTarget.addEventListener).toHaveBeenCalledTimes(1)
    expect(eventTarget.addEventListener).toHaveBeenCalledWith('keydown', expect.any(Function))

    view.unmount()

    expect(eventTarget.removeEventListener).toHaveBeenCalledTimes(1)
    expect(eventTarget.removeEventListener).toHaveBeenCalledWith(
      'keydown',
      eventTarget.addEventListener.mock.calls[0][1],
    )
  })

  it('matches a command shortcut before executing it', () => {
    const eventTarget = createEventTarget()
    const runtime = {
      commands: [{
        id: 'chat.new',
        shortcut: { key: 'n', ctrl: true, meta: false, alt: false, shift: false },
      }],
      execute: vi.fn(() => ({ executed: true, reason: '' })),
    }
    render(<DispatcherHarness eventTarget={eventTarget} runtime={runtime} />)

    const mismatch = { key: 'x', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false, preventDefault: vi.fn() }
    act(() => eventTarget.dispatch(mismatch))
    expect(runtime.execute).not.toHaveBeenCalled()
    expect(mismatch.preventDefault).not.toHaveBeenCalled()

    const match = { key: 'n', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false, preventDefault: vi.fn() }
    act(() => eventTarget.dispatch(match))
    expect(match.preventDefault).toHaveBeenCalledOnce()
    expect(runtime.execute).toHaveBeenCalledWith('chat.new')
  })

  it('does not prevent a matched shortcut when the command is disabled', () => {
    const eventTarget = createEventTarget()
    const runtime = {
      commands: [{
        id: 'turn.interrupt',
        shortcut: { key: 'escape', ctrl: false, meta: false, alt: false, shift: false },
      }],
      execute: vi.fn(() => ({ executed: false, reason: 'No active turn' })),
    }
    render(<DispatcherHarness eventTarget={eventTarget} runtime={runtime} />)

    const event = { key: 'Escape', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, preventDefault: vi.fn() }
    act(() => eventTarget.dispatch(event))

    expect(runtime.execute).toHaveBeenCalledWith('turn.interrupt')
    expect(event.preventDefault).not.toHaveBeenCalled()
  })
})

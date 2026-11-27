import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { memoryStorage } from '../test/memoryStorage'
import { setMediaQueryMatches } from '../test/mediaQueries'
import AppFrame from './AppFrame'

const narrowQuery = '(max-width: 767px)'
const darkQuery = '(prefers-color-scheme: dark)'

describe('AppFrame', () => {
  it('shows the preview banner above everything', () => {
    render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    expect(screen.getByText('Preview: the new web UI is incomplete')).toBeInTheDocument()
    expect(screen.getByText('sidebar')).toBeInTheDocument()
    expect(screen.getByText('conversation')).toBeInTheDocument()
  })

  it('collapses the sidebar to the rail and back', async () => {
    const user = userEvent.setup()
    const { container } = render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const frame = container.querySelector('[data-testid="app-frame"]')
    expect(frame?.getAttribute('data-collapsed')).toBe('false')
    await user.click(screen.getByRole('button', { name: 'Collapse sidebar' }))
    expect(frame?.getAttribute('data-collapsed')).toBe('true')
    await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
    expect(frame?.getAttribute('data-collapsed')).toBe('false')
  })

  it('opens the sidebar as a modal drawer below the breakpoint', async () => {
    const user = userEvent.setup()
    const original = window.matchMedia
    window.matchMedia = ((query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia
    try {
      const { container } = render(
        <AppFrame sidebar={<p>sidebar</p>}>
          <p>conversation</p>
        </AppFrame>,
      )
      const frame = container.querySelector('[data-testid="app-frame"]')
      expect(frame?.getAttribute('data-drawer')).toBe('false')
      await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
      expect(frame?.getAttribute('data-drawer')).toBe('true')
      expect(screen.getByRole('dialog', { name: 'Sessions' })).toBeInTheDocument()
      await user.keyboard('{Escape}')
      expect(frame?.getAttribute('data-drawer')).toBe('false')
    } finally {
      window.matchMedia = original
    }
  })

  it('opens the narrow drawer from a visible opener outside the panel', async () => {
    const user = userEvent.setup()
    setMediaQueryMatches(narrowQuery, true)
    render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const panel = screen.getByTestId('sidebar-panel')
    const opener = screen.getByRole('button', { name: 'Open sidebar' })
    expect(panel.contains(opener)).toBe(false)
    expect(opener.getAttribute('aria-controls')).toBe(panel.id)

    await user.click(opener)
    expect(screen.getByRole('dialog', { name: 'Sessions' })).toBeInTheDocument()

    await user.keyboard('{Escape}')
    expect(document.activeElement).toBe(opener)
    expect(panel.contains(document.activeElement)).toBe(false)
  })

  it('marks the conversation inert while the modal drawer is open', async () => {
    const user = userEvent.setup()
    setMediaQueryMatches(narrowQuery, true)
    const { container } = render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const main = container.querySelector('main')
    expect(main?.hasAttribute('inert')).toBe(false)

    await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
    expect(main?.hasAttribute('inert')).toBe(true)

    await user.keyboard('{Escape}')
    expect(main?.hasAttribute('inert')).toBe(false)
  })

  it('closes the drawer and drops the key handler when the window widens', async () => {
    const user = userEvent.setup()
    setMediaQueryMatches(narrowQuery, true)
    const { container } = render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const frame = container.querySelector('[data-testid="app-frame"]')
    await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
    expect(frame?.getAttribute('data-drawer')).toBe('true')

    act(() => setMediaQueryMatches(narrowQuery, false))
    expect(frame?.getAttribute('data-drawer')).toBe('false')

    const focused = document.activeElement
    await user.keyboard('{Escape}')
    expect(document.activeElement).toBe(focused)
  })

  it('traps Tab inside the drawer', async () => {
    const user = userEvent.setup()
    setMediaQueryMatches(narrowQuery, true)
    render(
      <AppFrame
        sidebar={
          <>
            <button type="button">one</button>
            <button type="button">two</button>
          </>
        }
      >
        <p>conversation</p>
      </AppFrame>,
    )
    await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
    const dialog = screen.getByRole('dialog', { name: 'Sessions' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Close sidebar' }))
    expect(dialog.contains(document.activeElement)).toBe(true)

    await user.click(screen.getByRole('button', { name: 'two' }))
    await user.tab()
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Close sidebar' }))

    await user.tab({ shift: true })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'two' }))
  })

  it('keeps Shift+Tab inside the drawer right after opening', async () => {
    const user = userEvent.setup()
    setMediaQueryMatches(narrowQuery, true)
    render(
      <AppFrame
        sidebar={
          <>
            <button type="button">one</button>
            <button type="button">two</button>
          </>
        }
      >
        <p>conversation</p>
      </AppFrame>,
    )
    await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
    const dialog = screen.getByRole('dialog', { name: 'Sessions' })

    await user.tab({ shift: true })
    expect(dialog.contains(document.activeElement)).toBe(true)

    screen.getByRole('button', { name: 'Open sidebar' }).focus()
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  })

  it('labels the resizer and points the wide toggle at the panel', () => {
    render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const panel = screen.getByTestId('sidebar-panel')
    expect(screen.getByRole('separator', { name: 'Resize sidebar' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Collapse sidebar' }).getAttribute('aria-controls')).toBe(
      panel.id,
    )
  })

  it('ignores a drag that does not start with the primary button', () => {
    const { container } = render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const frame = container.querySelector<HTMLElement>('[data-testid="app-frame"]')
    const separator = screen.getByRole('separator')
    fireEvent(separator, new MouseEvent('pointerdown', { bubbles: true, button: 2, clientX: 300 }))
    fireEvent(window, new MouseEvent('pointermove', { clientX: 360 }))
    fireEvent(window, new MouseEvent('pointerup'))
    expect(frame?.style.getPropertyValue('--crush-sidebar-width')).toBe('280px')
  })

  it('drops the drag listeners when the pointer is cancelled', () => {
    const { container } = render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const frame = container.querySelector<HTMLElement>('[data-testid="app-frame"]')
    const separator = screen.getByRole('separator')
    fireEvent(separator, new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 300 }))
    fireEvent(window, new MouseEvent('pointermove', { clientX: 330 }))
    expect(frame?.style.getPropertyValue('--crush-sidebar-width')).toBe('310px')

    fireEvent(window, new Event('pointercancel'))
    fireEvent(window, new MouseEvent('pointermove', { clientX: 400 }))
    expect(frame?.style.getPropertyValue('--crush-sidebar-width')).toBe('310px')
  })

  it('sets the sidebar width through a custom property on the frame', () => {
    const { container } = render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const frame = container.querySelector<HTMLElement>('[data-testid="app-frame"]')
    expect(frame?.style.getPropertyValue('--crush-sidebar-width')).toBe('280px')
    expect(frame?.style.width).toBe('')
  })

  it('applies the chosen theme and stores the preference', async () => {
    const user = userEvent.setup()
    const storage = memoryStorage()
    render(
      <AppFrame storage={storage} sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    expect(document.documentElement.dataset.dsTheme).toBe('light')

    await user.selectOptions(screen.getByRole('combobox', { name: 'Theme' }), 'dark')
    expect(storage.getItem('crush.theme')).toBe('dark')
    expect(document.documentElement.dataset.dsTheme).toBe('dark')

    await user.selectOptions(screen.getByRole('combobox', { name: 'Theme' }), 'light')
    expect(storage.getItem('crush.theme')).toBe('light')
    expect(document.documentElement.dataset.dsTheme).toBe('light')
  })

  it('starts on the stored preference', () => {
    render(
      <AppFrame storage={memoryStorage({ 'crush.theme': 'dark' })} sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    expect(document.documentElement.dataset.dsTheme).toBe('dark')
  })

  it('follows the system theme while the preference is system', () => {
    render(
      <AppFrame storage={memoryStorage()} sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    expect(document.documentElement.dataset.dsTheme).toBe('light')

    setMediaQueryMatches(darkQuery, true)
    expect(document.documentElement.dataset.dsTheme).toBe('dark')

    setMediaQueryMatches(darkQuery, false)
    expect(document.documentElement.dataset.dsTheme).toBe('light')
  })

  it('leaves an explicit preference alone when the system theme changes', () => {
    render(
      <AppFrame storage={memoryStorage({ 'crush.theme': 'light' })} sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    expect(document.documentElement.dataset.dsTheme).toBe('light')

    setMediaQueryMatches(darkQuery, true)
    expect(document.documentElement.dataset.dsTheme).toBe('light')
  })

  it('stops following the system theme when it unmounts', () => {
    const { unmount } = render(
      <AppFrame storage={memoryStorage()} sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    unmount()

    setMediaQueryMatches(darkQuery, true)
    expect(document.documentElement.dataset.dsTheme).toBe('light')
  })
})

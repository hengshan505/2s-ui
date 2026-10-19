// Shared overlay behaviors: Esc stack, body scroll lock, focus trap.
// All module-level singletons; overlay components opt in per open/close.

type EscHandler = () => void

// ---- Esc stack: only the top-most overlay reacts to Escape ----
const escStack: EscHandler[] = []

const onDocKeydown = (e: KeyboardEvent) => {
  // IME composition: Esc cancels the candidate list, never the overlay
  // (keyCode 229 covers engines that fire with isComposing already false)
  if (e.isComposing || e.keyCode === 229) return
  if (e.key !== 'Escape' || escStack.length === 0) return
  e.preventDefault()
  e.stopPropagation()
  escStack[escStack.length - 1]()
}

export const pushOverlay = (onEsc: EscHandler, opts?: { closeChildren?: boolean }): (() => void) => {
  if (escStack.length === 0) document.addEventListener('keydown', onDocKeydown, true)
  escStack.push(onEsc)
  let active = true
  return () => {
    if (!active) return
    active = false
    const i = escStack.lastIndexOf(onEsc)
    if (i >= 0 && opts?.closeChildren) {
      // a dialog closed programmatically (e.g. after async save) takes any
      // popovers opened above it down too — each child's close() pops itself
      const children = escStack.slice(i + 1)
      for (let k = children.length - 1; k >= 0; k--) children[k]()
    }
    const j = escStack.lastIndexOf(onEsc)
    if (j >= 0) escStack.splice(j, 1)
    if (escStack.length === 0) document.removeEventListener('keydown', onDocKeydown, true)
  }
}

// ---- Body scroll lock: ref-counted so stacked overlays don't unlock early ----
let lockCount = 0
let savedOverflow = ''
let savedPaddingRight = ''

export const lockScroll = (): (() => void) => {
  if (lockCount === 0) {
    const scrollbar = window.innerWidth - document.documentElement.clientWidth
    savedOverflow = document.body.style.overflow
    savedPaddingRight = document.body.style.paddingRight
    document.body.style.overflow = 'hidden'
    // compensate the scrollbar so the page doesn't shift
    if (scrollbar > 0) document.body.style.paddingRight = `${scrollbar}px`
  }
  lockCount++
  let active = true
  return () => {
    if (!active) return
    active = false
    if (--lockCount === 0) {
      document.body.style.overflow = savedOverflow
      document.body.style.paddingRight = savedPaddingRight
    }
  }
}

// ---- Focus trap: cycle Tab inside a panel, restore focus on release ----
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), ' +
  'select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

export const trapFocus = (container: HTMLElement): (() => void) => {
  const prev = document.activeElement as HTMLElement | null

  // focus the panel itself, not its first control, to avoid a stray focus ring.
  // The panel can still get one: Chrome matches :focus-visible on script focus
  // unless the last focus came from a mouse click, so whether the UA outline
  // was drawn around the whole dialog depended on what was clicked before
  // (#248). It is only a focus anchor; Tab moves on to controls with their own.
  container.tabIndex = -1
  container.style.outline = 'none'
  container.focus({ preventScroll: true })

  const focusables = () =>
    Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.offsetParent !== null,
    )

  const onKey = (e: KeyboardEvent) => {
    if (e.key !== 'Tab') return
    const els = focusables()
    if (els.length === 0) {
      e.preventDefault()
      return
    }
    const cur = document.activeElement as HTMLElement
    const i = els.indexOf(cur)
    const next = e.shiftKey
      ? i <= 0
        ? els.length - 1
        : i - 1
      : i < 0 || i === els.length - 1
        ? 0
        : i + 1
    e.preventDefault()
    els[next].focus()
  }
  container.addEventListener('keydown', onKey)

  return () => {
    container.removeEventListener('keydown', onKey)
    if (prev && prev.isConnected) prev.focus({ preventScroll: true })
    else if (container.contains(document.activeElement)) (document.activeElement as HTMLElement).blur()
  }
}

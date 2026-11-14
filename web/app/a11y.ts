// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// announce: a short message for screen readers, read when they are idle, through a live region of
// the module's own (the app's announcer isn't one of the parts it shares with modules).
let region: HTMLElement | null = null

export function announce(text: string) {
  if (typeof document === 'undefined') return
  if (!region || !region.isConnected) {
    region = document.createElement('div')
    region.setAttribute('role', 'status')
    region.setAttribute('aria-live', 'polite')
    region.className = 'tvs-announce'
    document.body.appendChild(region)
  }
  region.textContent = ''
  const el = region
  setTimeout(() => { el.textContent = text }, 50)
}

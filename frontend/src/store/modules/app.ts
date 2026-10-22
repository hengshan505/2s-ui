import { defineStore } from 'pinia'
import { i18n, intlLocale } from '@/locales'

export type Theme = 'dark' | 'light' | 'system'
export type ResolvedTheme = 'dark' | 'light'

const THEME_KEY = '2sui-theme'
const RTL_LOCALES = new Set(['fa'])

const darkMedia = window.matchMedia('(prefers-color-scheme: dark)')

// 'system' tracks the OS preference; any other value is taken literally
function resolveTheme(theme: Theme): ResolvedTheme {
  if (theme === 'system') return darkMedia.matches ? 'dark' : 'light'
  return theme
}

function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = resolveTheme(theme)
}

// lang is what picks CJK glyphs and a screen reader's voice, so a page left at
// "en" renders Chinese with whatever the fallback font prefers. Set before the
// app mounts, so nothing is painted under the wrong one.
function applyLang() {
  const root = document.documentElement
  root.lang = intlLocale()
  root.dir = RTL_LOCALES.has(i18n.global.locale.value) ? 'rtl' : 'ltr'
}

const AppStore = defineStore('App', {
  state: () => ({
    theme: (localStorage.getItem(THEME_KEY) as Theme) || 'system',
    sidebarOpen: false, // mobile overlay sidebar
  }),
  getters: {
    isRtl: () => RTL_LOCALES.has(i18n.global.locale.value),
    // the dark/light actually painted (resolves 'system' to the OS preference)
    resolvedTheme: (state): ResolvedTheme => resolveTheme(state.theme),
  },
  actions: {
    init() {
      applyTheme(this.theme)
      applyLang()
      // re-paint when the OS flips its theme, but only while following the system
      darkMedia.addEventListener('change', () => {
        if (this.theme === 'system') applyTheme('system')
      })
    },
    setTheme(theme: Theme) {
      this.theme = theme
      localStorage.setItem(THEME_KEY, theme)
      applyTheme(theme)
    },
    toggleTheme() {
      // cycle light → dark → system (kept for any non-menu callers)
      const order: Theme[] = ['light', 'dark', 'system']
      this.setTheme(order[(order.indexOf(this.theme) + 1) % order.length])
    },
    setLocale(l: string) {
      i18n.global.locale.value = l as any
      localStorage.setItem('locale', l)
      applyLang()
    },
  },
})

export default AppStore

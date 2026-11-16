/** Live tab controls shared by the Browser and command palette. No page content is copied. */
export interface BrowserTab {
  id: string
  url: string
  title?: string
}
export interface BrowserTabGroup {
  tabs: BrowserTab[]
  selected: string
  select: (id: string) => void
  close: (id: string) => void
  add: (url?: string) => void
}
export const browserTabGroups = new Map<string, BrowserTabGroup>()
export const TABS_CHANGED = "basalisk:tabs-changed"
export const tabsChanged = () => window.dispatchEvent(new Event(TABS_CHANGED))

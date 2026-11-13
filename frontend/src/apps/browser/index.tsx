/**
 * Browser: real web pages in a desktop window, drawn by a native WebKit view the desktop app
 * places over the window's body (macshell/browser_darwin.m).
 */
import { Globe, Search } from "lucide-react"
import { toURL } from "../../api/browser"
import { registerApp, registerCommands, registerProvider } from "../../desktop/registry"
import { BrowserWindow } from "./window"
import "./browser.css"

registerApp({
  id: "browser",
  title: "Browser",
  icon: Globe,
  menu: true,
  size: [1180, 780],
  bare: true,
  multi: true,
  render: ({ winId, args }) => <BrowserWindow winId={winId} args={args} />,
})

registerCommands([
  {
    id: "browser.new",
    title: "Browser: new window",
    section: "Browser",
    icon: Globe,
    keywords: "web internet safari",
    keys: "ctrl+alt+b",
    run: (desk) => desk.open("browser"),
  },
])

// Typing an address or a search in the palette opens it.
registerProvider({
  id: "browser",
  search(query) {
    const q = query.trim()
    if (q.length < 2) return []
    const url = toURL(q)
    const isSearch = url.startsWith("https://duckduckgo.com/?q=")
    return [
      {
        id: `browser:${url}`,
        title: isSearch ? `Search the web for “${q}”` : `Open ${url}`,
        section: "Web",
        icon: isSearch ? Search : Globe,
        run: (desk) => desk.open("browser", { args: { url } }),
      },
    ]
  },
})

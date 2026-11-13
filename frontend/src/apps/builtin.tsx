import {
  Cable,
  History,
  Inbox,
  Keyboard,
  Laptop,
  type LucideIcon,
  Monitor,
  Palette,
  Radio,
  Settings,
  Shield,
  SquareTerminal,
  User,
} from "lucide-react"
import { type ReactNode, useState } from "react"
import { useDesk } from "../desktop/context"
import { type AppDef, registerApp } from "../desktop/registry"
import { useDeskSettings } from "../desktop/settings"
import { Shortcuts } from "../desktop/shortcuts"
import { THEMES } from "../desktop/themes"
import { AccountPage } from "../pages/account"
import { DevicesPage } from "../pages/devices"
import { IntegrationsPage } from "../pages/integrations"
import { OutboxPage } from "../pages/outbox"
import { ReadinessPage } from "../pages/readiness"
import { SecurityPage } from "../pages/security"
import { SessionsPage } from "../pages/sessions"
import { ShellPage } from "../pages/shell"
import { useSession } from "../session"
import { TerminalSlot, useTerminalHost } from "../terminal/host"

function Timezone({ children }: { children: (timezone: string) => ReactNode }) {
  return <>{children(useSession().profile.timezone)}</>
}

function TerminalApp() {
  const { enabled } = useTerminalHost()
  if (enabled === null) return <p className="desk-note">Opening the terminal…</p>
  if (!enabled)
    return (
      <p className="desk-note">
        This Basalisk does not hand out a shell. The terminal runs in the desktop app.
      </p>
    )
  return <TerminalSlot owner="page" className="terminal-slot" />
}

function Appearance() {
  const desk = useDesk()
  const active = useDeskSettings().theme
  return (
    <div className="desk-themes">
      {THEMES.map((t) => (
        <button
          key={t.id}
          type="button"
          className="desk-theme"
          data-active={t.id === active}
          onClick={() => desk.setTheme(t.id)}
        >
          <span className="desk-theme-preview" style={{ background: t.desk }}>
            <span className="desk-theme-window" style={{ background: t.bg, borderColor: t.accent }}>
              <span style={{ color: t.accent }}>~</span>{" "}
              <span style={{ color: t.fg }}>basalisk</span>{" "}
              <span style={{ color: t.accent2 }}>✓</span>
            </span>
            <span className="desk-theme-swatches">
              {[1, 2, 3, 4, 5, 6].map((i) => (
                <span key={i} style={{ background: t.ansi[i] }} />
              ))}
            </span>
          </span>
          <span className="desk-theme-name">{t.name}</span>
        </button>
      ))}
    </div>
  )
}

const SECTIONS: { id: string; label: string; icon: LucideIcon; render: () => ReactNode }[] = [
  { id: "appearance", label: "Appearance", icon: Palette, render: () => <Appearance /> },
  { id: "shortcuts", label: "Shortcuts", icon: Keyboard, render: () => <Shortcuts /> },
  { id: "account", label: "Account", icon: User, render: () => <AccountPage /> },
  { id: "security", label: "Security", icon: Shield, render: () => <SecurityPage /> },
  {
    id: "integrations",
    label: "Integrations",
    icon: Cable,
    render: () => <Timezone>{(tz) => <IntegrationsPage timezone={tz} />}</Timezone>,
  },
  {
    id: "devices",
    label: "Devices",
    icon: Laptop,
    render: () => <Timezone>{(tz) => <DevicesPage timezone={tz} />}</Timezone>,
  },
  { id: "shell", label: "Mac shell", icon: Monitor, render: () => <ShellPage /> },
]

function SettingsApp() {
  const [section, setSection] = useState("appearance")
  const current = SECTIONS.find((s) => s.id === section) ?? SECTIONS[0]
  return (
    <div className="desk-settings">
      <nav className="desk-settings-nav" aria-label="Settings sections">
        {SECTIONS.map((s) => (
          <button
            key={s.id}
            type="button"
            data-active={s.id === section}
            onClick={() => setSection(s.id)}
          >
            <s.icon aria-hidden="true" size={15} strokeWidth={1.6} />
            {s.label}
          </button>
        ))}
      </nav>
      <div className="desk-settings-body" key={current.id}>
        <h2 className="desk-settings-title">{current.label}</h2>
        {current.render()}
      </div>
    </div>
  )
}

const BUILTIN: AppDef[] = [
  {
    id: "terminal",
    title: "Terminal",
    icon: SquareTerminal,
    menu: true,
    size: [980, 620],
    bare: true,
    render: () => <TerminalApp />,
  },
  {
    id: "sessions",
    title: "Work sessions",
    icon: History,
    menu: true,
    size: [920, 640],
    render: () => <Timezone>{(tz) => <SessionsPage timezone={tz} />}</Timezone>,
  },
  {
    id: "outbox",
    title: "Fragment outbox",
    icon: Inbox,
    menu: true,
    size: [860, 600],
    render: () => <OutboxPage />,
  },
  {
    id: "readiness",
    title: "Capture readiness",
    icon: Radio,
    menu: true,
    size: [860, 600],
    render: () => <Timezone>{(tz) => <ReadinessPage timezone={tz} />}</Timezone>,
  },
  {
    id: "settings",
    title: "Settings",
    icon: Settings,
    menu: false,
    size: [1040, 680],
    bare: true,
    render: () => <SettingsApp />,
  },
]
for (const app of BUILTIN) registerApp(app)

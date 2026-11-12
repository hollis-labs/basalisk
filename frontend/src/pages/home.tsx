import { Cable, History, Settings, Waypoints } from "lucide-react"
import { useSession } from "../session"

export function HomePage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { profile } = useSession()
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">YOUR WORKSPACE</p>
        <h2>Welcome, {profile.displayName}.</h2>
        <p>A home for the things you want to build, connect, and keep.</p>
      </div>
      <div className="welcome-panel">
        <Waypoints aria-hidden="true" className="h-8 w-8 text-primary" />
        <h2>Start with your foundations.</h2>
        <p>
          Your workspace is ready. Personalize your account, then add the services your tools will
          use.
        </p>
      </div>
      <div className="home-grid">
        <button type="button" className="panel destination" onClick={() => onNavigate("/sessions")}>
          <History aria-hidden="true" />
          <h2>Follow your work</h2>
          <p>Start a session and keep your context together.</p>
          <span>Open work sessions →</span>
        </button>
        <button type="button" className="panel destination" onClick={() => onNavigate("/account")}>
          <Settings aria-hidden="true" />
          <h2>Make it yours</h2>
          <p>Profile, appearance, and timezone.</p>
          <span>Open account →</span>
        </button>
        <button
          type="button"
          className="panel destination"
          onClick={() => onNavigate("/integrations")}
        >
          <Cable aria-hidden="true" />
          <h2>Connect your services</h2>
          <p>Keep API credentials in one place.</p>
          <span>Manage integrations →</span>
        </button>
      </div>
    </div>
  )
}

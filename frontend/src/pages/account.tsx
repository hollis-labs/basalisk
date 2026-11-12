import { AccountProfile } from "@hollis-labs/kit-account"
import { type SettingsDraft, type SettingsGroup, SettingsRenderer } from "@hollis-labs/kit-settings"
import { useState } from "react"
import { loadAccount, request } from "../api/account"
import { Notice } from "../components/forms"
import { useSession } from "../session"

const preferences: SettingsGroup = {
  id: "preferences",
  label: "Preferences",
  schema: {
    type: "object",
    additionalProperties: false,
    required: ["theme", "timezone"],
    properties: {
      theme: { type: "string", title: "Appearance", enum: ["dark", "light", "system"] },
      timezone: {
        type: "string",
        title: "Timezone",
        description: "IANA timezone, such as America/Chicago.",
        minLength: 1,
        maxLength: 100,
      },
    },
  },
  fields: {
    theme: { editable: true, secret: false, restart_required: false },
    timezone: { editable: true, secret: false, restart_required: false },
  },
  capabilities: { can_read: true, can_update: true, can_validate: true, can_reset: false },
}

export function AccountPage() {
  const { profile, setProfile } = useSession()
  const [draft, setDraft] = useState({ displayName: profile.displayName, email: profile.email })
  const [settingsDraft, setSettingsDraft] = useState<SettingsDraft>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  // Profile (name, email) belongs to the identity module; preferences to the app. Both saves
  // are followed by a reload of /api/account so the shown profile and its revision stay true.
  async function save(write: () => Promise<unknown>) {
    setBusy(true)
    setError("")
    setMessage("")
    try {
      await write()
      setProfile(await loadAccount())
      setSettingsDraft({})
      setMessage("Changes saved.")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save changes.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">ADMINISTRATION</p>
        <h2>Your account</h2>
        <p>Make this workspace feel like yours.</p>
      </div>
      <Notice error={error} message={message} />
      <div className="account-grid">
        <div className="stack">
          <AccountProfile
            value={draft}
            onValueChange={setDraft}
            onSave={(value) =>
              void save(() =>
                request("identity/profile", "PUT", {
                  display_name: value.displayName,
                  email: value.email,
                }),
              )
            }
            saving={busy}
          />
          <div className="panel">
            <h2>Workspace owner</h2>
            <p>
              Signed in as <strong>{profile.username}</strong>.
            </p>
            <p className="text-fg-muted">
              This account can manage all of this workspace’s settings and connections.
            </p>
          </div>
        </div>
        <SettingsRenderer
          contractVersion={1}
          groups={[preferences]}
          states={{
            preferences: {
              draft: settingsDraft,
              busy,
              values: {
                theme: { present: true, value: profile.theme, editable: true, has_override: true },
                timezone: {
                  present: true,
                  value: profile.timezone,
                  editable: true,
                  has_override: true,
                },
              },
            },
          }}
          onDraftChange={(_, value) => setSettingsDraft(value)}
          onSave={(_, changes) =>
            void save(() =>
              request("preferences", "PUT", {
                theme: profile.theme,
                timezone: profile.timezone,
                ...changes.set,
                revision: profile.revision,
              }),
            )
          }
        />
      </div>
    </div>
  )
}

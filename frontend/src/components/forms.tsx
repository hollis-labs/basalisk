import { type ReactNode, useId } from "react"
export function Field({ label, children }: { label: string; children: (id: string) => ReactNode }) {
  const id = useId()
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {children(id)}
    </div>
  )
}

export function Notice({ error, message }: { error?: string; message?: string }) {
  return (
    <>
      {error && (
        <p className="notice text-danger" role="alert">
          {error}
        </p>
      )}
      {message && (
        <p className="notice text-success" role="status">
          {message}
        </p>
      )}
    </>
  )
}

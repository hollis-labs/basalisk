import { constantTrust, defineBindingTable, resolve } from "@hollis-labs/design-bindings"
import { genuiBinding, parseEnvelope } from "@hollis-labs/genui"

const table = defineBindingTable({
  contractDigest: "800ceba78dca0e34ef1c68fc5eb1900122d9acea1648b930d20dad389af052af",
  resolverConfig: { inlinePayloadLimitBytes: 256 << 10 },
  trust: constantTrust("host-primitive"),
  defaults: [genuiBinding],
  rows: [],
})

/** Treat every surface as untrusted declarative content, including host-produced examples. */
export function admitGenui(raw: string) {
  const envelope = parseEnvelope(raw)
  const result = resolve(table, envelope.type, { version: envelope.typeVersion, origin: "agent" })
  if (
    !result.ok ||
    result.binding.rendererId !== genuiBinding.rendererId ||
    new TextEncoder().encode(raw).byteLength > result.inlinePayloadLimitBytes
  )
    throw new Error("This surface has no admitted renderer.")
  return envelope
}

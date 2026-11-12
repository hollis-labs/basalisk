import type { FrontendManifest, ManifestItem } from "@hollis-labs/app-shell"
import contributions from "./app.contributions.json"
import generated from "./generated/frontend.manifest.json"

/** The app's own module name in the merged manifest. Substrate has no app-level contribution seam. */
export const APP_MODULE = "basalisk"

/** The builder's default priority for a contribution that names none. */
const DEFAULT_PRIORITY = 10

interface Fragment {
  module: string
  contributions: Omit<ManifestItem, "module">[]
}

/**
 * The shell reads one manifest. Substrate builds `generated/frontend.manifest.json` from the
 * modules the binary links; none of them contributes Basalisk's pages, and the builder reads
 * ui.json only from catalogue modules. So the app's own surfaces live in app.contributions.json
 * (same item shape as a module's ui.json) and are appended here, tagged with the app module.
 */
export function buildManifest(base: FrontendManifest, fragment: Fragment): FrontendManifest {
  const priority = (i: ManifestItem) => i.priority ?? DEFAULT_PRIORITY
  const items = [
    // The builder writes `items: null` when no module contributes any; treat it as empty.
    ...(base.items ?? []),
    ...fragment.contributions.map((c) => ({ ...c, module: fragment.module })),
  ].sort((a, b) => priority(a) - priority(b)) // Array.sort is stable
  return { ...base, items }
}

export const manifest: FrontendManifest = buildManifest(
  generated as unknown as FrontendManifest,
  contributions as Fragment,
)

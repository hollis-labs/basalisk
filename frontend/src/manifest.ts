import type { FrontendManifest } from "@hollis-labs/app-shell"
import generated from "./generated/frontend.manifest.json"

/**
 * The shell reads one manifest. `substrate frontend` builds it from the modules the binary links
 * and from the app's own contribution file (`frontend.contributions` in cmd/basalisk/substrate.yaml),
 * validated and merged with the modules' items, so there is nothing to merge here.
 */
export const manifest: FrontendManifest = generated as unknown as FrontendManifest

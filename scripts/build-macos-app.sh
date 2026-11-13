#!/usr/bin/env bash
# Build Basalisk.app: the desktop shell (the server, a native window, the macOS shell layer
# and the terminal, in one process).
#
#   scripts/build-macos-app.sh [--no-ui] [--install] [--open]
#
#   --no-ui     reuse internal/webui/dist instead of rebuilding the frontend
#   --install   copy the app to /Applications (replacing the one there)
#   --open      open the app when done
#
# A development copy that can run beside the real app:
#   BASALISK_BUNDLE_ID=com.hollislabs.basalisk.dev BASALISK_APP_NAME="Basalisk Dev" scripts/build-macos-app.sh
#   open -g "build/Basalisk Dev.app" --env BASALISK_DATA_DIR=/some/scratch --env SUBSTRATE_CONFIG=/some/dev.yaml
#
# Signing: macOS attaches the Accessibility and Input Monitoring grants to the app's code
# signature. An ad-hoc signature changes with every build, so each rebuild loses the grants;
# a real identity keeps them. BASALISK_SIGN_IDENTITY names the identity to use. Without it
# the first "Developer ID Application" identity in the keychain is used, and ad-hoc ("-")
# when there is none.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

ui=1 install=0 open_app=0
for arg in "$@"; do
	case "$arg" in
	--no-ui) ui=0 ;;
	--install) install=1 ;;
	--open) open_app=1 ;;
	*) echo "unknown option: $arg" >&2; exit 2 ;;
	esac
done

# BASALISK_BUNDLE_ID and BASALISK_APP_NAME build a second, separate app (for instance
# com.hollislabs.basalisk.dev, "Basalisk Dev"): its own web view storage and its own privacy
# grants, so it can run beside the real one without sharing its sign-in.
bundle_id="${BASALISK_BUNDLE_ID:-com.hollislabs.basalisk}"
app_name="${BASALISK_APP_NAME:-Basalisk}"
version="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
app="$root/build/$app_name.app"

if [ "$ui" = 1 ]; then
	(cd frontend && npm ci && npm run build)
fi

rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

CGO_ENABLED=1 go build -tags desktop -trimpath \
	-ldflags "-s -w" \
	-o "$app/Contents/MacOS/basalisk" ./cmd/basalisk

if [ -f "$root/scripts/macos/Basalisk.icns" ]; then
	cp "$root/scripts/macos/Basalisk.icns" "$app/Contents/Resources/Basalisk.icns"
fi

cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>${app_name}</string>
	<key>CFBundleDisplayName</key><string>${app_name}</string>
	<key>CFBundleIdentifier</key><string>${bundle_id}</string>
	<key>CFBundleExecutable</key><string>basalisk</string>
	<key>CFBundleIconFile</key><string>Basalisk</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${version}</string>
	<key>CFBundleVersion</key><string>${version}</string>
	<key>LSMinimumSystemVersion</key><string>12.0</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.productivity</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSSupportsAutomaticGraphicsSwitching</key><true/>
	<key>NSAppTransportSecurity</key>
	<dict>
		<key>NSAllowsLocalNetworking</key><true/>
	</dict>
</dict>
</plist>
PLIST

identity="${BASALISK_SIGN_IDENTITY:-}"
if [ -z "$identity" ]; then
	identity="$(security find-identity -v -p codesigning 2>/dev/null |
		sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)"
fi
[ -n "$identity" ] || identity="-"
codesign --force --sign "$identity" --identifier "$bundle_id" "$app"
codesign --verify --strict "$app"
if [ "$identity" = "-" ]; then
	echo "signed ad-hoc: permission grants will not survive a rebuild (set BASALISK_SIGN_IDENTITY)"
else
	echo "signed with a keychain identity: permission grants survive rebuilds"
fi

if [ "$install" = 1 ]; then
	rm -rf "/Applications/$app_name.app"
	cp -R "$app" "/Applications/$app_name.app"
	app="/Applications/$app_name.app"
fi
echo "built $app ($version)"
if [ "$open_app" = 1 ]; then
	open "$app"
fi

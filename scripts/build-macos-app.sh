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
#
# Passkeys in the Browser (off unless asked for): Apple lets a browser make passkey requests
# for any site only with the com.apple.developer.web-browser.public-key-credential
# entitlement, which Apple grants on request and delivers in a provisioning profile.
# BASALISK_PROVISION_PROFILE names that profile (a Developer ID profile for this bundle id
# with the entitlement in it). With it the app declares http and https links (one of
# Apple's conditions for a browser), the profile is embedded, and the app is signed with the
# entitlements the profile grants and the hardened runtime. Without it nothing changes.
# A differently signed app can lose its Accessibility and Input Monitoring grants: build it
# as the dev copy first. docs/browser-passkeys.md has the steps.
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

profile="${BASALISK_PROVISION_PROFILE:-}"
passkeys=0
if [ -n "$profile" ]; then
	[ -f "$profile" ] || { echo "BASALISK_PROVISION_PROFILE: no file at $profile" >&2; exit 2; }
	work="$(mktemp -d)"
	trap 'rm -rf "$work"' EXIT
	if ! security cms -D -i "$profile" >"$work/profile.plist" 2>/dev/null; then
		echo "BASALISK_PROVISION_PROFILE: $profile is not a provisioning profile" >&2
		exit 2
	fi
	pl() { /usr/libexec/PlistBuddy -c "Print :$1" "$work/profile.plist" 2>/dev/null || true; }
	team="$(pl 'TeamIdentifier:0')"
	appid="$(pl 'Entitlements:com.apple.application-identifier')"
	granted="$(pl 'Entitlements:com.apple.developer.web-browser.public-key-credential')"
	expires="$(pl 'ExpirationDate')"
	if [ "$granted" != "true" ]; then
		echo "BASALISK_PROVISION_PROFILE: the profile does not grant com.apple.developer.web-browser.public-key-credential" >&2
		exit 2
	fi
	if [ "$appid" != "$team.$bundle_id" ]; then
		echo "BASALISK_PROVISION_PROFILE: the profile is for $appid, not $team.$bundle_id" >&2
		exit 2
	fi
	cat >"$work/app.entitlements" <<ENT
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>com.apple.application-identifier</key><string>${appid}</string>
	<key>com.apple.developer.team-identifier</key><string>${team}</string>
	<key>com.apple.developer.web-browser.public-key-credential</key><true/>
</dict>
</plist>
ENT
	passkeys=1
	echo "passkeys: profile for $appid (expires $expires)"
fi

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

if [ "$passkeys" = 1 ]; then
	# A browser opens http and https links (Apple's first condition for the entitlement).
	/usr/libexec/PlistBuddy \
		-c "Add :CFBundleURLTypes array" \
		-c "Add :CFBundleURLTypes:0 dict" \
		-c "Add :CFBundleURLTypes:0:CFBundleURLName string Web site URL" \
		-c "Add :CFBundleURLTypes:0:CFBundleTypeRole string Viewer" \
		-c "Add :CFBundleURLTypes:0:CFBundleURLSchemes array" \
		-c "Add :CFBundleURLTypes:0:CFBundleURLSchemes:0 string http" \
		-c "Add :CFBundleURLTypes:0:CFBundleURLSchemes:1 string https" \
		"$app/Contents/Info.plist"
	cp "$profile" "$app/Contents/embedded.provisionprofile"
fi

identity="${BASALISK_SIGN_IDENTITY:-}"
if [ -z "$identity" ]; then
	identity="$(security find-identity -v -p codesigning 2>/dev/null |
		sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)"
fi
[ -n "$identity" ] || identity="-"
if [ "$passkeys" = 1 ]; then
	[ "$identity" != "-" ] || { echo "passkeys need a Developer ID identity, not ad-hoc signing" >&2; exit 2; }
	codesign --force --sign "$identity" --identifier "$bundle_id" --options runtime \
		--entitlements "$work/app.entitlements" "$app"
else
	codesign --force --sign "$identity" --identifier "$bundle_id" "$app"
fi
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

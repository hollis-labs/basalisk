#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/basalisk-companion"
SIGNING_CONFIG="$CONFIG_DIR/signing-identity"
SIGNING_IDENTITY="${BASALISK_SIGNING_IDENTITY:-}"
SIGNING_KEYCHAIN="${BASALISK_SIGNING_KEYCHAIN:-}"
SIGNING_PASSWORD_FILE="${BASALISK_SIGNING_PASSWORD_FILE:-}"
if [[ -z "$SIGNING_IDENTITY" && -f "$SIGNING_CONFIG" ]]; then
  SIGNING_IDENTITY="$(cat "$SIGNING_CONFIG")"
fi
if [[ -z "$SIGNING_KEYCHAIN" && -f "$CONFIG_DIR/signing-keychain" ]]; then
  SIGNING_KEYCHAIN="$(cat "$CONFIG_DIR/signing-keychain")"
fi
if [[ -z "$SIGNING_PASSWORD_FILE" && -f "$CONFIG_DIR/signing-password-file" ]]; then
  SIGNING_PASSWORD_FILE="$(cat "$CONFIG_DIR/signing-password-file")"
fi
if [[ -z "$SIGNING_IDENTITY" || "$SIGNING_IDENTITY" == "-" ]]; then
  echo "Set BASALISK_SIGNING_IDENTITY or $SIGNING_CONFIG to a code-signing certificate name or fingerprint." >&2
  echo "Use a persistent identity so Keychain can recognize the companion across rebuilds." >&2
  exit 1
fi
SIGNING_ARGS=(--force --sign "$SIGNING_IDENTITY" --identifier "com.hollislabs.basalisk.companion" --timestamp=none --options runtime)
if [[ -n "$SIGNING_KEYCHAIN" ]]; then
  SIGNING_ARGS+=(--keychain "$SIGNING_KEYCHAIN")
fi
if [[ -n "$SIGNING_PASSWORD_FILE" ]]; then
  if [[ -z "$SIGNING_KEYCHAIN" || ! -r "$SIGNING_PASSWORD_FILE" ]]; then
    echo "A readable signing password file requires an explicit signing Keychain." >&2
    exit 1
  fi
  # Dedicated local signing Keychains can have a generated password unrelated
  # to the login password. Follow the local signing setup; never trace secrets.
  set +x
  security unlock-keychain -p "$(<"$SIGNING_PASSWORD_FILE")" "$SIGNING_KEYCHAIN"
fi
swift build -c release
app="build/Basalisk Companion.app"
mkdir -p "$app/Contents/MacOS"
cp "$(swift build -c release --show-bin-path)/BasaliskCompanion" "$app/Contents/MacOS/BasaliskCompanion"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>BasaliskCompanion</string>
<key>CFBundleIdentifier</key><string>com.hollislabs.basalisk.companion</string>
<key>CFBundleName</key><string>Basalisk Companion</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>LSUIElement</key><false/>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict></plist>
PLIST
codesign "${SIGNING_ARGS[@]}" "$app"
codesign --verify --deep --strict "$app"
printf '%s\n' "Built $app"

#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/basalisk-companion"
SIGNING_IDENTITY="${BASALISK_SIGNING_IDENTITY:-$(cat "$CONFIG_DIR/signing-identity")}"
SIGNING_KEYCHAIN="${BASALISK_SIGNING_KEYCHAIN:-$(cat "$CONFIG_DIR/signing-keychain")}"
SIGNING_PASSWORD_FILE="${BASALISK_SIGNING_PASSWORD_FILE:-$(cat "$CONFIG_DIR/signing-password-file")}"
set +x
security unlock-keychain -p "$(<"$SIGNING_PASSWORD_FILE")" "$SIGNING_KEYCHAIN"
CHECK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/basalisk-xpc.XXXXXX")"
DOMAIN="gui/$(id -u)"
LABEL="com.hollislabs.basalisk.check.$RANDOM.$RANDOM"
cleanup() { launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true; rm -rf "$CHECK_DIR"; }
trap cleanup EXIT
swiftc -swift-version 5 -parse-as-library \
 Sources/BasaliskCompanion/Transport.swift Sources/BasaliskCompanion/LocalContext.swift \
 Sources/BasaliskCompanion/Model.swift Sources/BasaliskCompanion/CaptureBridge.swift \
 Sources/BasaliskCompanion/CaptureOutbox.swift Checks/CaptureBridgeChecks.swift \
 -o "$CHECK_DIR/helper"
for spec in 'server com.hollislabs.basalisk.companion' 'client com.hollis-labs.frag' 'impostor com.example.impostor'; do
 read -r name identifier <<< "$spec"
 cp "$CHECK_DIR/helper" "$CHECK_DIR/$name"
 codesign --force --sign "$SIGNING_IDENTITY" --keychain "$SIGNING_KEYCHAIN" --identifier "$identifier" --timestamp=none --options runtime "$CHECK_DIR/$name"
done
start_server() {
 /usr/bin/python3 - "$CHECK_DIR" "$LABEL" "$1" <<'PY'
import plistlib,sys
root,label,binary=sys.argv[1:]
with open(root+'/agent.plist','wb') as f:
 plistlib.dump({'Label':label,'ProgramArguments':[root+'/'+binary,'server',label,'unused'],'MachServices':{label:True},'RunAtLoad':True},f)
PY
 launchctl bootstrap "$DOMAIN" "$CHECK_DIR/agent.plist"
}
start_server server
"$CHECK_DIR/client" client "$LABEL" accept
"$CHECK_DIR/impostor" client "$LABEL" reject
launchctl bootout "$DOMAIN/$LABEL"
start_server impostor
"$CHECK_DIR/client" client "$LABEL" reject

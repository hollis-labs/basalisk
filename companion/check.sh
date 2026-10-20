#!/bin/sh
set -eu
cd "$(dirname "$0")"
mkdir -p .build/checks
# Command Line Tools include AppKit/Security but may omit XCTest and Testing.
swiftc -swift-version 5 -parse-as-library \
  Sources/BasaliskCompanion/Transport.swift \
  Sources/BasaliskCompanion/LocalContext.swift \
  Sources/BasaliskCompanion/CaptureBridge.swift \
  Sources/BasaliskCompanion/CaptureOutbox.swift \
  Sources/BasaliskCompanion/Model.swift Checks/CompanionChecks.swift \
  -o .build/checks/companion-checks
if [ "${1:-}" != "--gui" ]; then
  exec .build/checks/companion-checks
fi
# SSH's security session cannot interact with the login Keychain. Launch these
# isolated checks in the logged-in desktop session, just like the real app.
app="$PWD/.build/checks/CompanionChecks.app"
mkdir -p "$app/Contents/MacOS"
cp .build/checks/companion-checks "$app/Contents/MacOS/companion-checks"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>companion-checks</string>
<key>CFBundleIdentifier</key><string>com.hollislabs.basalisk.companion.checks</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
</dict></plist>
PLIST
codesign --force --sign - "$app"
: > .build/checks/stdout
: > .build/checks/stderr
rm -f .build/checks/result
open -W -n -g --stdout "$PWD/.build/checks/stdout" --stderr "$PWD/.build/checks/stderr" \
  --env "BASALISK_CHECK_RESULT=$PWD/.build/checks/result" \
  --env "BASALISK_TEST_ORIGIN=${BASALISK_TEST_ORIGIN:-}" "$app"
cat .build/checks/stdout .build/checks/stderr
test "$(cat .build/checks/result)" = 0

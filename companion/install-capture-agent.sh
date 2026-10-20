#!/bin/bash
set -euo pipefail
# Run after installing the signed bundle and quitting the old companion.
# No credentials are stored in the LaunchAgent. The user must still approve
# Frag in the companion and grant the device capture permission in Basalisk.
APP_PATH="${BASALISK_COMPANION_APP:-$HOME/Applications/Basalisk Companion.app}"
LABEL="com.hollislabs.basalisk.companion"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
codesign --verify --strict "$APP_PATH"
if pgrep -x BasaliskCompanion >/dev/null; then
  echo 'Quit Basalisk Companion before installing its capture LaunchAgent.' >&2
  exit 1
fi
mkdir -p "$HOME/Library/LaunchAgents"
/usr/bin/python3 - "$APP_PATH" "$PLIST" <<'PY'
import os, plistlib, sys, tempfile
app, path = sys.argv[1:]
value = {
 'Label': 'com.hollislabs.basalisk.companion',
 'ProgramArguments': [app + '/Contents/MacOS/BasaliskCompanion'],
 'MachServices': {'com.hollislabs.basalisk.companion.capture': True},
 'EnvironmentVariables': {'BASALISK_CAPTURE_SERVICE': '1'},
 'LimitLoadToSessionType': 'Aqua', 'RunAtLoad': True,
 'ProcessType': 'Interactive',
}
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path))
with os.fdopen(fd, 'wb') as f: plistlib.dump(value, f)
os.replace(tmp, path)
PY
# Bootstrap creates the per-user service. A previously installed agent should
# be booted out before replacing its bundle, through its current manager.
launchctl bootstrap "gui/$(id -u)" "$PLIST"
echo 'Companion capture agent installed. Enable Frag access in its window.'

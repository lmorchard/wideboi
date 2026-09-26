#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "desktop-app requires macOS" >&2
  exit 1
fi

binary="${1:?binary path required}"
bundle="${2:?app bundle path required}"
mkdir -p "$bundle/Contents/MacOS"
cp "$binary" "$bundle/Contents/MacOS/wideboi-desktop"
chmod 755 "$bundle/Contents/MacOS/wideboi-desktop"
cat > "$bundle/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>io.github.lmorchard.wideboi</string>
  <key>CFBundleName</key><string>wideboi</string>
  <key>CFBundleDisplayName</key><string>wideboi</string>
  <key>CFBundleExecutable</key><string>wideboi-desktop</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF

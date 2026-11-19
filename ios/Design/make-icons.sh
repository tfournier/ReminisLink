#!/bin/sh
# Renders the app icon and the launch screen mark from the SVG files of
# AppIcon/ into the asset catalog of the app. Needs only macOS and Xcode
# (AppKit renders the SVG). Run it from any folder after changing an SVG,
# then commit the SVG and the PNG files together.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
assets="$here/../ReminisLink/Resources/Assets.xcassets"
render="$here/render-svg.swift"

icon="$assets/AppIcon.appiconset"
swift "$render" "$here/AppIcon/icon.svg" "$icon/AppIcon.png" 1024 opaque
swift "$render" "$here/AppIcon/icon-dark.svg" "$icon/AppIcon-Dark.png" 1024 opaque
swift "$render" "$here/AppIcon/icon-tinted.svg" "$icon/AppIcon-Tinted.png" 1024 opaque

logo="$assets/LaunchLogo.imageset"
swift "$render" "$here/AppIcon/launch-logo.svg" "$logo/LaunchLogo.png" 200 alpha
swift "$render" "$here/AppIcon/launch-logo.svg" "$logo/LaunchLogo@2x.png" 400 alpha
swift "$render" "$here/AppIcon/launch-logo.svg" "$logo/LaunchLogo@3x.png" 600 alpha

echo "Icons written to $assets"

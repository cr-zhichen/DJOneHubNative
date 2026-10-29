#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/dev-common.sh"
assert_dev_identity

CONFIGURATION="${FLAME_CONFIGURATION:-Debug}"
ARCH="${FLAME_ARCH:-$(uname -m)}"
BUILT_APP="$DERIVED_DATA/Build/Products/$CONFIGURATION/DJOneHub.app"

command -v xcodebuild >/dev/null 2>&1 || die "未找到 xcodebuild"
/usr/bin/security find-identity -v -p codesigning | /usr/bin/grep -F "$SIGNING_IDENTITY" >/dev/null \
  || die "未找到可用签名身份：$SIGNING_IDENTITY"

mkdir -p "$BUILD_ROOT/Artifacts"
log "隔离构建：configuration=$CONFIGURATION arch=$ARCH"
xcodebuild \
  -project "$REPO_ROOT/DJOneHubNative.xcodeproj" \
  -scheme DJOneHubNative \
  -configuration "$CONFIGURATION" \
  -destination 'platform=macOS' \
  -derivedDataPath "$DERIVED_DATA" \
  "ARCHS=$ARCH" \
  "PRODUCT_BUNDLE_IDENTIFIER=$BUNDLE_ID" \
  "DISPLAY_NAME=$DISPLAY_NAME" \
  CODE_SIGN_STYLE=Manual \
  "CODE_SIGN_IDENTITY=$SIGNING_IDENTITY" \
  build

[ -d "$BUILT_APP" ] || die "Xcode 构建成功但未找到产物：$BUILT_APP"
rm -rf "$ARTIFACT_APP"
/usr/bin/ditto "$BUILT_APP" "$ARTIFACT_APP"
verify_app_static "$ARTIFACT_APP"
log "构建产物已验证：$ARTIFACT_APP"
printf '%s\n' "$ARTIFACT_APP"

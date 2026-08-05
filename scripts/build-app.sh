#!/bin/sh
set -eu

# 构建 DJOneHubNative.app 原型：
#   1. 编译 Go 后端（djonehubd，Unix socket 监听）
#   2. swiftc 编译 SwiftUI 前端
#   3. 组装 .app，并携带 libusb 动态库

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
APP_NAME="DJOneHubNative"
DIST_DIR="${ROOT_DIR}/dist"
BUILD_DIR="${ROOT_DIR}/build"
APP_DIR="${DIST_DIR}/${APP_NAME}.app"
SDK="${SDKROOT:-$(xcrun --sdk macosx --show-sdk-path)}"

TARGET_OS="arm64-apple-macosx13.0"

echo "==> 1/4 编译 Go 后端"
cd "${ROOT_DIR}/backend"
PKG_CONFIG_PATH="${PKG_CONFIG_PATH:-/opt/homebrew/lib/pkgconfig:/usr/local/lib/pkgconfig}"
export PKG_CONFIG_PATH
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build \
  -trimpath -ldflags="-s -w" \
  -o "${BUILD_DIR}/djonehubd" ./cmd/djonehub-macos

echo "==> 2/4 编译 SwiftUI 前端"
mkdir -p "${BUILD_DIR}"
cd "${ROOT_DIR}/app"
swiftc \
  -O \
  -target "${TARGET_OS}" \
  -sdk "${SDK}" \
  -framework SwiftUI -framework AppKit -framework Combine \
  -o "${BUILD_DIR}/DJOneHubNative" \
  Sources/DJOneHubNativeApp.swift \
  Sources/BackendProcess.swift \
  Sources/DashboardStore.swift \
  Sources/AudioBridge.swift \
  Sources/UnixSocketURLProtocol.swift \
  Sources/APIClient.swift \
  Sources/Models.swift \
  Sources/Views/ContentView.swift \
  Sources/Views/HomeView.swift \
  Sources/Views/SMSView.swift \
  Sources/Views/ESIMView.swift \
  Sources/Views/DiagnosticsView.swift

echo "==> 3/4 组装 .app"
rm -rf "${APP_DIR}"
mkdir -p "${APP_DIR}/Contents/MacOS"
mkdir -p "${APP_DIR}/Contents/Resources/backend"
cp "${BUILD_DIR}/DJOneHubNative" "${APP_DIR}/Contents/MacOS/"
cp "${BUILD_DIR}/djonehubd" "${APP_DIR}/Contents/Resources/backend/"
cp "${ROOT_DIR}/app/Resources/Info.plist" "${APP_DIR}/Contents/"

# libusb：拷贝 brew 的 dylib 进 app，并把后端的链接指向 @loader_path
LIBUSB_SRC="/opt/homebrew/opt/libusb/lib/libusb-1.0.0.dylib"
if [ -f "${LIBUSB_SRC}" ]; then
  cp "${LIBUSB_SRC}" "${APP_DIR}/Contents/Resources/backend/"
  install_name_tool -change "${LIBUSB_SRC}" "@loader_path/libusb-1.0.0.dylib" \
    "${APP_DIR}/Contents/Resources/backend/djonehubd" 2>/dev/null || true
fi

echo "==> 4/4 完成"
echo "  ${APP_DIR}"

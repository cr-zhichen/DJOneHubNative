#!/bin/sh
set -eu

# 构建 DJOneHubNative.app：
#   用法: ./scripts/build-app.sh [arm64 | x86_64 | universal]   （默认 arm64）
#   环境变量:
#     LIBUSB_DYLIB    libusb-1.0.0.dylib 路径（需含目标架构；universal 需通用版）
#     PKG_CONFIG_PATH 含 libusb-1.0.pc 的 pkg-config 搜索路径

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
APP_NAME="DJOneHubNative"
DIST_DIR="${ROOT_DIR}/dist"
BUILD_DIR="${ROOT_DIR}/build"
SDK="${SDKROOT:-$(xcrun --sdk macosx --show-sdk-path)}"

ARCH="${1:-$(uname -m)}"
case "$ARCH" in
  arm64|x86_64|universal) ;;
  *) echo "usage: $0 [arm64 | x86_64 | universal]" >&2; exit 1 ;;
esac

PKG_CONFIG_PATH="${PKG_CONFIG_PATH:-/opt/homebrew/lib/pkgconfig:/usr/local/lib/pkgconfig}"
export PKG_CONFIG_PATH

goarch() { [ "$1" = "x86_64" ] && echo "amd64" || echo "$1"; }
swifttarget() { [ "$1" = "x86_64" ] && echo "x86_64-apple-macosx13.0" || echo "arm64-apple-macosx13.0"; }

resolve_libusb() {
  if [ -z "${LIBUSB_DYLIB:-}" ]; then
    [ "$ARCH" = "arm64" ] || { echo "错误：构建 $ARCH 必须设置 LIBUSB_DYLIB" >&2; exit 1; }
    LIBUSB_DYLIB="/opt/homebrew/opt/libusb/lib/libusb-1.0.0.dylib"
    [ -f "$LIBUSB_DYLIB" ] || LIBUSB_DYLIB="/usr/local/opt/libusb/lib/libusb-1.0.0.dylib"
    [ -f "$LIBUSB_DYLIB" ] || { echo "错误：未找到 brew 的 libusb，请设置 LIBUSB_DYLIB" >&2; exit 1; }
    echo "    使用 brew libusb: $LIBUSB_DYLIB"
  fi
  info=$(lipo -info "$LIBUSB_DYLIB" 2>/dev/null || true)
  case "$ARCH" in
    universal)
      echo "$info" | grep -q "arm64"  || { echo "错误：libusb 缺少 arm64 架构" >&2; exit 1; }
      echo "$info" | grep -q "x86_64" || { echo "错误：libusb 缺少 x86_64 架构" >&2; exit 1; } ;;
    *)
      echo "$info" | grep -q "$ARCH" || { echo "错误：libusb 缺少 $ARCH 架构" >&2; exit 1; } ;;
  esac
}

build_one_arch() {
  arch="$1"
  out="${BUILD_DIR}/arch/${arch}"
  mkdir -p "$out"
  echo "==> 编译 Go 后端 (${arch})"
  cd "${ROOT_DIR}/backend"
  CGO_ENABLED=1 GOOS=darwin GOARCH="$(goarch "$arch")" go build \
    -trimpath -a -ldflags="-s -w" \
    -o "${out}/djonehubd" ./cmd/djonehub-macos
  echo "==> 编译 SwiftUI 前端 (${arch})"
  cd "${ROOT_DIR}/app"
  swiftc \
    -O \
    -target "$(swifttarget "$arch")" \
    -sdk "${SDK}" \
    -framework SwiftUI -framework AppKit -framework Combine \
    -o "${out}/DJOneHubNative" \
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
}

assemble_app() {
  if [ "$ARCH" = "universal" ]; then
    APP_DIR="${DIST_DIR}/${APP_NAME}.app"
    echo "==> 合并通用二进制 (lipo)"
    mkdir -p "${BUILD_DIR}/universal"
    lipo -create \
      "${BUILD_DIR}/arch/arm64/djonehubd" "${BUILD_DIR}/arch/x86_64/djonehubd" \
      -output "${BUILD_DIR}/universal/djonehubd"
    lipo -create \
      "${BUILD_DIR}/arch/arm64/DJOneHubNative" "${BUILD_DIR}/arch/x86_64/DJOneHubNative" \
      -output "${BUILD_DIR}/universal/DJOneHubNative"
    SRC="${BUILD_DIR}/universal"
  else
    APP_DIR="${DIST_DIR}/${APP_NAME}-${ARCH}.app"
    SRC="${BUILD_DIR}/arch/${ARCH}"
  fi

  echo "==> 组装 .app: ${APP_DIR}"
  rm -rf "${APP_DIR}"
  mkdir -p "${APP_DIR}/Contents/MacOS"
  mkdir -p "${APP_DIR}/Contents/Resources/backend"
  cp "${SRC}/DJOneHubNative" "${APP_DIR}/Contents/MacOS/"
  cp "${SRC}/djonehubd" "${APP_DIR}/Contents/Resources/backend/"
  cp "${ROOT_DIR}/app/Resources/Info.plist" "${APP_DIR}/Contents/"

  # libusb：拷贝进 app，并把后端对它的引用改为 @loader_path
  cp "${LIBUSB_DYLIB}" "${APP_DIR}/Contents/Resources/backend/libusb-1.0.0.dylib"
  old=$(otool -L "${APP_DIR}/Contents/Resources/backend/djonehubd" 2>/dev/null | awk '/libusb/ {print $1; exit}')
  if [ -n "$old" ]; then
    install_name_tool -change "$old" "@loader_path/libusb-1.0.0.dylib" \
      "${APP_DIR}/Contents/Resources/backend/djonehubd" 2>/dev/null || true
  fi
}

resolve_libusb
case "$ARCH" in
  arm64|x86_64) build_one_arch "$ARCH" ;;
  universal)    build_one_arch arm64; build_one_arch x86_64 ;;
esac
assemble_app

echo "==> 完成"
echo "  ${APP_DIR}"

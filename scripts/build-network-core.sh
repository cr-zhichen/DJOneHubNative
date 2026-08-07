#!/bin/sh
set -eu

SING_BOX_VERSION="1.13.16"
SING_BOX_BUILD_VERSION="1.13.16-djonehub.1"
SING_BOX_COMMIT="17ec3c71af8ca946dc50bf0d927c39fc77322aec"
SING_BOX_TAGS="with_gvisor"
MINIMUM_MACOS="13.0"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
PATCH_FILE="${SCRIPT_DIR}/patches/sing-box-1.13.16-djonehub.patch"

if [ "$#" -lt 3 ]; then
  echo "usage: build-network-core.sh OUTPUT_DIR CACHE_DIR ARCH [ARCH ...]" >&2
  exit 2
fi

OUTPUT_DIR="$1"
CACHE_DIR="$2"
shift 2
ARCH_LIST="$*"
SOURCE_DIR="${CACHE_DIR}/sing-box-${SING_BOX_VERSION}"
BUILD_DIR="${CACHE_DIR}/build"

mkdir -p "${OUTPUT_DIR}" "${CACHE_DIR}" "${BUILD_DIR}"

if [ ! -d "${SOURCE_DIR}/.git" ]; then
  git clone --quiet --depth 1 --branch "v${SING_BOX_VERSION}" \
    https://github.com/SagerNet/sing-box.git "${SOURCE_DIR}"
fi

ACTUAL_COMMIT=$(git -C "${SOURCE_DIR}" rev-parse HEAD)
if [ "${ACTUAL_COMMIT}" != "${SING_BOX_COMMIT}" ]; then
  echo "错误：sing-box 源码提交不匹配，期望 ${SING_BOX_COMMIT}，实际 ${ACTUAL_COMMIT}" >&2
  echo "请清理 Xcode DerivedData 后重试。" >&2
  exit 1
fi

[ -f "${PATCH_FILE}" ] || { echo "错误：缺少 sing-box DJOneHub 补丁" >&2; exit 1; }
if git -C "${SOURCE_DIR}" apply --unidiff-zero --reverse --check "${PATCH_FILE}" >/dev/null 2>&1; then
  :
else
  git -C "${SOURCE_DIR}" apply --unidiff-zero --check "${PATCH_FILE}" || {
    echo "错误：无法应用 sing-box DJOneHub 补丁，请清理 Xcode DerivedData 后重试。" >&2
    exit 1
  }
  git -C "${SOURCE_DIR}" apply --unidiff-zero "${PATCH_FILE}"
fi
PATCH_SHA=$(shasum -a 256 "${PATCH_FILE}" | awk '{print $1}')

build_core() {
  APPLE_ARCH="$1"
  case "${APPLE_ARCH}" in
    arm64)
      GO_ARCH="arm64"
      OVERRIDE_BINARY="${SING_BOX_BINARY_ARM64:-}"
      ;;
    x86_64)
      GO_ARCH="amd64"
      OVERRIDE_BINARY="${SING_BOX_BINARY_X86_64:-}"
      ;;
    *)
      echo "错误：不支持的网络核心架构 ${APPLE_ARCH}" >&2
      exit 1
      ;;
  esac

  ARCH_DIR="${BUILD_DIR}/${APPLE_ARCH}"
  CORE_BINARY="${ARCH_DIR}/sing-box"
  PATCH_STAMP="${ARCH_DIR}/djonehub-patch.sha256"
  mkdir -p "${ARCH_DIR}"

  if [ -n "${OVERRIDE_BINARY}" ]; then
    [ -f "${OVERRIDE_BINARY}" ] || { echo "错误：网络核心不存在：${OVERRIDE_BINARY}" >&2; exit 1; }
    cp "${OVERRIDE_BINARY}" "${CORE_BINARY}"
  elif [ ! -x "${CORE_BINARY}" ] || [ "${DJONEHUB_REBUILD_NETWORK_CORE:-0}" = "1" ] || \
    [ "$(cat "${PATCH_STAMP}" 2>/dev/null || true)" != "${PATCH_SHA}" ]; then
    CGO_ENABLED=1 \
    GOOS=darwin \
    GOARCH="${GO_ARCH}" \
    MACOSX_DEPLOYMENT_TARGET="${MINIMUM_MACOS}" \
    CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=${MINIMUM_MACOS}" \
    CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=${MINIMUM_MACOS}" \
      go -C "${SOURCE_DIR}" build -trimpath -tags "${SING_BOX_TAGS}" \
        -ldflags="-X github.com/sagernet/sing-box/constant.Version=${SING_BOX_BUILD_VERSION} -s -w" \
        -o "${CORE_BINARY}" ./cmd/sing-box
    printf '%s\n' "${PATCH_SHA}" > "${PATCH_STAMP}"
  fi

  chmod 755 "${CORE_BINARY}"
  lipo "${CORE_BINARY}" -verify_arch "${APPLE_ARCH}"
  MINOS=$(vtool -show-build "${CORE_BINARY}" | awk '$1 == "minos" { print $2; exit }')
  if [ "${MINOS}" != "${MINIMUM_MACOS}" ]; then
    echo "错误：${CORE_BINARY} 最低系统版本为 ${MINOS:-未知}，期望 ${MINIMUM_MACOS}" >&2
    exit 1
  fi
}

for ARCH in ${ARCH_LIST}; do
  build_core "${ARCH}"
done

ARCH_COUNT=$(printf '%s\n' ${ARCH_LIST} | wc -l | tr -d ' ')
if [ "${ARCH_COUNT}" -gt 1 ]; then
  [ -x "${BUILD_DIR}/arm64/sing-box" ] || { echo "错误：缺少 arm64 网络核心" >&2; exit 1; }
  [ -x "${BUILD_DIR}/x86_64/sing-box" ] || { echo "错误：缺少 x86_64 网络核心" >&2; exit 1; }
  lipo -create "${BUILD_DIR}/arm64/sing-box" "${BUILD_DIR}/x86_64/sing-box" \
    -output "${OUTPUT_DIR}/sing-box"
else
  cp "${BUILD_DIR}/${ARCH_LIST}/sing-box" "${OUTPUT_DIR}/sing-box"
fi

chmod 755 "${OUTPUT_DIR}/sing-box"
cp "${SOURCE_DIR}/LICENSE" "${OUTPUT_DIR}/sing-box-GPL-3.0-or-later.txt"

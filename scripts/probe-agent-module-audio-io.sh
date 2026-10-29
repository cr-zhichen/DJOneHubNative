#!/bin/bash
# Offline PCM conversion/frame tests for the CoreAudio adapter; does not open devices.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
out="${TMPDIR:-/tmp}/djonehub-module-audio-io-probe"
swiftc "$root/app/Sources/AgentModuleCoreAudioIO.swift" \
  "$root/app/Sources/AgentAudioTransport.swift" \
  "$root/app/Sources/APIClient.swift" \
  "$root/app/Sources/UnixSocketURLProtocol.swift" \
  "$root/app/Sources/LocalBackendAuthentication.swift" \
  "$root/app/Sources/NativeSpeechTranscriber.swift" \
  "$root/app/Tests/AgentModuleAudioIOProbe/main.swift" \
  -framework CoreAudio -framework CryptoKit -framework AVFoundation -framework Speech -o "$out"
exec "$out"

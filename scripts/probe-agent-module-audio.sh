#!/bin/bash
# Read-only CoreAudio/UAC device discovery; does not open streams or alter USB state.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
out="${TMPDIR:-/tmp}/djonehub-module-audio-probe"
swiftc "$root/app/Sources/AgentModuleCoreAudioIO.swift" \
  "$root/app/Sources/AgentAudioTransport.swift" \
  "$root/app/Sources/APIClient.swift" \
  "$root/app/Sources/UnixSocketURLProtocol.swift" \
  "$root/app/Sources/LocalBackendAuthentication.swift" \
  "$root/app/Sources/NativeSpeechTranscriber.swift" \
  "$root/app/Tests/AgentModuleAudioProbe/main.swift" \
  -framework CoreAudio -framework CryptoKit -framework AVFoundation -framework Speech -o "$out"
exec "$out"

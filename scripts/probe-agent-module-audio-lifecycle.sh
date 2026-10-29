#!/bin/sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
out="${TMPDIR:-/tmp}/djonehub-module-audio-lifecycle-probe"
swiftc -parse-as-library "$root/app/Sources/AgentModuleCoreAudioIO.swift" \
  "$root/app/Sources/AgentAudioTransport.swift" \
  "$root/app/Sources/APIClient.swift" \
  "$root/app/Sources/UnixSocketURLProtocol.swift" \
  "$root/app/Sources/LocalBackendAuthentication.swift" \
  "$root/app/Sources/NativeSpeechTranscriber.swift" \
  "$root/app/Tests/AgentModuleAudioLifecycleProbe/main.swift" \
  -framework CoreAudio -framework CryptoKit -framework AVFoundation -framework Speech -o "$out"
exec "$out"

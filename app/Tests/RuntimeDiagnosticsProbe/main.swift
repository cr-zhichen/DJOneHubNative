import Foundation

@main
struct RuntimeDiagnosticsProbe {
    static func main() {
        let activeCall = CallStatus(
            state: "active",
            number: "10000000000",
            incoming: false,
            active: true,
            sessionID: "session-sensitive",
            dtmfAvailable: true,
            postDialState: nil,
            postDialError: nil)
        let verification = VoiceVerificationAudioStatus(
            active: false,
            expiresAt: nil,
            allowedNumbers: ["10000000000"],
            answered: true,
            consumed: true,
            audioAuthorized: true,
            sessionID: "session-sensitive",
            lastError: nil)

        let missingChain = RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: false,
            statusStale: false,
            moduleConnected: true,
            callModeState: "ready",
            callModeSummary: "通话模式已就绪",
            callStatus: activeCall,
            verification: verification,
            agentAudio: makeAgentAudio(
                clientConnected: false,
                moduleIOReady: false,
                captureActive: false,
                framesReceived: 0,
                nonzeroFrames: 0,
                asrRunning: false,
                asrError: nil,
                transcript: "验证码是 123456",
                verificationCode: "123456"),
            agentStatusReadFailed: false,
            generatedAt: Date(timeIntervalSince1970: 1_700_000_000))

        require(missingChain.overallLevel == .warning, "active authorized call without Agent chain must warn")
        require(missingChain.item(id: "agent-client")?.level == .warning, "missing client must warn")
        require(missingChain.item(id: "module-io")?.level == .warning, "missing module IO must warn")

        let readyChain = RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: false,
            statusStale: false,
            moduleConnected: true,
            callModeState: "ready",
            callModeSummary: "通话模式已就绪",
            callStatus: activeCall,
            verification: verification,
            agentAudio: makeAgentAudio(
                clientConnected: true,
                moduleIOReady: true,
                captureActive: true,
                framesReceived: 508,
                nonzeroFrames: 499,
                asrRunning: true,
                asrError: nil,
                transcript: "验证码是 123456",
                verificationCode: "123456"),
            agentStatusReadFailed: false,
            generatedAt: Date(timeIntervalSince1970: 1_700_000_000))

        require(readyChain.overallLevel == .healthy, "complete active chain must be healthy")
        require(readyChain.item(id: "pcm-capture")?.level == .healthy, "nonzero capture must be healthy")
        require(readyChain.item(id: "asr")?.level == .healthy, "running ASR must be healthy")

        let idle = RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: false,
            statusStale: false,
            moduleConnected: true,
            callModeState: "ready",
            callModeSummary: "通话模式已就绪",
            callStatus: CallStatus(state: "idle", number: nil, incoming: false, active: false),
            verification: .inactive,
            agentAudio: makeAgentAudio(
                clientConnected: false,
                moduleIOReady: false,
                captureActive: false,
                framesReceived: 0,
                nonzeroFrames: 0,
                asrRunning: false,
                asrError: nil,
                transcript: nil,
                verificationCode: nil),
            agentStatusReadFailed: false,
            generatedAt: Date(timeIntervalSince1970: 1_700_000_000))

        require(idle.overallLevel == .healthy, "expected idle Agent state must not be treated as failure")
        require(idle.item(id: "agent-client")?.level == .neutral, "idle disconnected client must be neutral")

        let mismatchedVerification = VoiceVerificationAudioStatus(
            active: false,
            expiresAt: nil,
            allowedNumbers: ["10000000000"],
            answered: true,
            consumed: true,
            audioAuthorized: true,
            sessionID: "old-session",
            lastError: nil)
        let mismatchedSession = RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: false,
            statusStale: false,
            moduleConnected: true,
            callModeState: "ready",
            callModeSummary: "ready",
            callStatus: activeCall,
            verification: mismatchedVerification,
            agentAudio: makeAgentAudio(
                clientConnected: false,
                moduleIOReady: false,
                captureActive: false,
                framesReceived: 0,
                nonzeroFrames: 0,
                asrRunning: false,
                asrError: nil,
                transcript: nil,
                verificationCode: nil),
            agentStatusReadFailed: false)
        require(mismatchedSession.item(id: "agent-client")?.level == .neutral,
                "stale verification session must not require Agent chain")

        let missingSessionCall = CallStatus(state: "active", number: nil, incoming: false, active: true)
        let missingSession = RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: false,
            statusStale: false,
            moduleConnected: true,
            callModeState: "ready",
            callModeSummary: "ready",
            callStatus: missingSessionCall,
            verification: verification,
            agentAudio: makeAgentAudio(
                clientConnected: false,
                moduleIOReady: false,
                captureActive: false,
                framesReceived: 0,
                nonzeroFrames: 0,
                asrRunning: false,
                asrError: nil,
                transcript: nil,
                verificationCode: nil),
            agentStatusReadFailed: false)
        require(missingSession.item(id: "agent-client")?.level == .neutral,
                "missing call session must not require Agent chain")

        let optionalAgent = RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: false,
            statusStale: false,
            moduleConnected: true,
            callModeState: "ready",
            callModeSummary: "ready",
            callStatus: CallStatus(state: "idle", number: nil, incoming: false, active: false),
            verification: .inactive,
            agentAudio: makeAgentAudio(
                compiled: false,
                clientConnected: false,
                moduleIOReady: false,
                captureActive: false,
                framesReceived: 0,
                nonzeroFrames: 0,
                asrRunning: false,
                asrError: nil,
                transcript: nil,
                verificationCode: nil),
            agentStatusReadFailed: false)
        require(optionalAgent.overallLevel == .healthy,
                "optional uncompiled Agent capability must not fail idle diagnostics")
        require(optionalAgent.item(id: "agent-transport")?.level == .neutral,
                "optional uncompiled transport must be neutral while idle")

        let hostileSummary = "phone 10000000000 IMEI 350225641234561 session session-sensitive code 123456 /Users/example/agent-audio.sock"
        let hostileReport = RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: false,
            statusStale: false,
            moduleConnected: true,
            callModeState: "failed",
            callModeSummary: hostileSummary,
            callStatus: CallStatus(state: "idle", number: nil, incoming: false, active: false),
            verification: .inactive,
            agentAudio: nil,
            agentStatusReadFailed: false).sanitizedReport
        for sensitive in ["10000000000", "350225641234561", "session-sensitive", "123456", "/Users/example"] {
            require(!hostileReport.contains(sensitive), "hostile summary leaked sensitive value: \(sensitive)")
        }

        let report = readyChain.sanitizedReport
        for sensitive in ["10000000000", "session-sensitive", "123456", "验证码是"] {
            require(!report.contains(sensitive), "report leaked sensitive value: \(sensitive)")
        }
        require(report.contains("PCM 采集"), "report must contain diagnostic rows")
        require(report.contains("508 帧"), "report must retain non-sensitive counters")

        print("PASS runtime diagnostics classification and privacy-safe export")
    }

    private static func makeAgentAudio(
        compiled: Bool = true,
        clientConnected: Bool,
        moduleIOReady: Bool,
        captureActive: Bool,
        framesReceived: UInt64,
        nonzeroFrames: UInt64,
        asrRunning: Bool,
        asrError: String?,
        transcript: String?,
        verificationCode: String?
    ) -> AgentAudioStatus {
        AgentAudioStatus(
            compiled: compiled,
            transportReady: compiled,
            moduleIOReady: moduleIOReady,
            pcmTransportAvailable: compiled,
            voiceAgentAvailable: clientConnected && moduleIOReady,
            active: clientConnected,
            socketPath: "/Users/example/Library/Application Support/FlameDJHub/agent-audio.sock",
            clientConnected: clientConnected,
            captureActive: captureActive,
            captureSessionID: "session-sensitive",
            framesReceived: framesReceived,
            nonzeroFrames: nonzeroFrames,
            bytesReceived: framesReceived * 320,
            peakSample: nonzeroFrames > 0 ? 32768 : 0,
            captureError: nil,
            lastCapture: nil,
            asrAvailable: asrRunning && asrError == nil,
            asrCompiled: true,
            asrRunning: asrRunning,
            asrError: asrError,
            ttsAvailable: false,
            format: AgentAudioFormatStatus(
                encoding: "pcm_s16le", sampleRate: 8000, channels: 1,
                frameMilliseconds: 20, frameBytes: 320),
            error: nil,
            transcript: transcript,
            transcriptFinal: transcript != nil,
            verificationCode: verificationCode)
    }

    private static func require(_ condition: @autoclosure () -> Bool, _ message: String) {
        guard condition() else {
            fputs("FAIL: \(message)\n", stderr)
            exit(1)
        }
    }
}

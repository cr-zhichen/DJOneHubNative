import Foundation

/// Serializes an async status poll and rejects responses from an older generation.
struct PollGenerationGate {
    private(set) var generation: UInt64 = 0
    private(set) var inFlight = false

    mutating func begin() -> UInt64? {
        guard !inFlight else { return nil }
        generation &+= 1
        inFlight = true
        return generation
    }

    mutating func invalidate() {
        generation &+= 1
        inFlight = false
    }

    func isCurrent(_ token: UInt64) -> Bool {
        token == generation
    }

    mutating func accept(_ token: UInt64) -> Bool {
        guard inFlight, token == generation else { return false }
        inFlight = false
        return true
    }
}

enum DashboardFailClosedPolicy {
    static func voiceVerificationAfterReadFailure(_ error: Error) -> VoiceVerificationAudioStatus {
        _ = error
        return .inactive
    }

    static func agentAudioAfterReadFailure(_ error: Error) -> AgentAudioStatus? {
        _ = error
        return nil
    }
}

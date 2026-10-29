import Foundation

@main
struct VoiceVerificationAudioPolicyProbe {
    static func main() {
        precondition(VoiceVerificationAudioPolicy.reservesAgentAudio(
            callSessionID: "session-a",
            callActive: true,
            verification: VoiceVerificationAudioStatus(
                active: true,
                expiresAt: nil,
                allowedNumbers: [],
                answered: true,
                consumed: true,
                audioAuthorized: true,
                sessionID: "session-a",
                lastError: nil
            )
        ))
        precondition(!VoiceVerificationAudioPolicy.reservesAgentAudio(
            callSessionID: "session-b",
            callActive: true,
            verification: VoiceVerificationAudioStatus(
                active: true,
                expiresAt: nil,
                allowedNumbers: [],
                answered: true,
                consumed: true,
                audioAuthorized: true,
                sessionID: "session-a",
                lastError: nil
            )
        ))
        precondition(!VoiceVerificationAudioPolicy.reservesAgentAudio(
            callSessionID: "session-a",
            callActive: false,
            verification: VoiceVerificationAudioStatus(
                active: true,
                expiresAt: nil,
                allowedNumbers: [],
                answered: true,
                consumed: true,
                audioAuthorized: true,
                sessionID: "session-a",
                lastError: nil
            )
        ))
        precondition(!VoiceVerificationAudioPolicy.reservesAgentAudio(
            callSessionID: "session-a",
            callActive: true,
            verification: VoiceVerificationAudioStatus(
                active: true,
                expiresAt: nil,
                allowedNumbers: [],
                answered: true,
                consumed: true,
                audioAuthorized: false,
                sessionID: "session-a",
                lastError: nil
            )
        ))
        print("PASS voice-verification Agent audio reservation policy")
    }
}

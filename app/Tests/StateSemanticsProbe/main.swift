import Foundation

@main
struct StateSemanticsProbe {
    private struct ProbeError: LocalizedError {
        var errorDescription: String? { "probe failure" }
    }

    static func main() {
        var voice = PollGenerationGate()
        let oldPoll = voice.begin()!
        voice.invalidate()
        let armPoll = voice.begin()!
        precondition(!voice.accept(oldPoll), "an old voice poll must not win after arm/disarm invalidation")
        precondition(voice.accept(armPoll), "the current voice poll must be accepted")

        var audio = PollGenerationGate()
        let first = audio.begin()!
        precondition(audio.begin() == nil, "agent-audio polling must not overlap")
        audio.invalidate()
        precondition(!audio.accept(first), "invalidated agent-audio poll must not publish")

        let failedVoice = DashboardFailClosedPolicy.voiceVerificationAfterReadFailure(ProbeError())
        precondition(!failedVoice.active)
        precondition(!failedVoice.audioAuthorized)
        precondition(failedVoice.sessionID == nil)

        precondition(DashboardFailClosedPolicy.agentAudioAfterReadFailure(ProbeError()) == nil)

        var resultText: String?
        var errorText: String?
        NativeSpeechTranscriberErrorPolicy.report(
            ProbeError(),
            onResult: { text, _ in resultText = text },
            onError: { errorText = $0 })
        precondition(resultText == nil, "runtime ASR errors must never become transcripts")
        precondition(errorText == "probe failure")
        print("PASS state semantics, fail-closed status, and native ASR error policy")
    }
}

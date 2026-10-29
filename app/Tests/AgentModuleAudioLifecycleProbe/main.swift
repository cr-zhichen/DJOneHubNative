import Foundation

@main
struct AgentModuleCoreAudioLifecycleProbe {
    static func main() {
        var readiness = AgentModuleIOReadiness()
        readiness.beginStart()
        readiness.markStarted(.capture)
        precondition(!readiness.markCallback(.capture), "capture alone became ready")
        readiness.markStarted(.playback)
        precondition(!readiness.isReady, "started endpoints without playback callback became ready")
        precondition(readiness.markCallback(.playback), "both callbacks did not become ready")
        precondition(readiness.isReady, "ready state missing")
        readiness.reset()
        precondition(!readiness.isReady, "stop did not revoke readiness")

        readiness.beginStart()
        readiness.markStarted(.capture)
        _ = readiness.markCallback(.capture)
        readiness.reset()
        precondition(!readiness.markCallback(.playback), "failed second endpoint left stale readiness")
        print("PASS lifecycle readiness requires both starts and both callbacks, and reset revokes it")

        let io = CoreAudioAgentModuleAudioIO()
        let ready = DispatchSemaphore(value: 0)
        let uplinkLock = NSLock()
        var uplinkFrames = 0

        io.onReady = {
            ready.signal()
        }

        do {
            fputs("stage=start-1\n", stderr)
            try io.start(
                pullDownlink: { Data(count: AgentAudioProtocol.pcmFrameBytes) },
                pushUplink: { frame in
                    guard frame.count == AgentAudioProtocol.pcmFrameBytes else {
                        fputs("FAIL unexpected uplink frame size: \(frame.count)\n", stderr)
                        exit(2)
                    }
                    uplinkLock.lock()
                    uplinkFrames += 1
                    uplinkLock.unlock()
                }
            )
            fputs("stage=wait-ready-1\n", stderr)
            guard ready.wait(timeout: .now() + 3) == .success else {
                fputs("FAIL CoreAudio callbacks did not become ready\n", stderr)
                io.stop()
                exit(3)
            }
            fputs("stage=ready-1\n", stderr)
            Thread.sleep(forTimeInterval: 0.25)
            fputs("stage=stop-1\n", stderr)
            io.stop()
            fputs("stage=stopped-1\n", stderr)

            // A second start verifies that stop removed both IOProcs and reset state.
            let secondReady = DispatchSemaphore(value: 0)
            io.onReady = { secondReady.signal() }
            fputs("stage=start-2\n", stderr)
            try io.start(
                pullDownlink: { Data(count: AgentAudioProtocol.pcmFrameBytes) },
                pushUplink: { _ in }
            )
            fputs("stage=wait-ready-2\n", stderr)
            guard secondReady.wait(timeout: .now() + 3) == .success else {
                fputs("FAIL CoreAudio callbacks did not recover after restart\n", stderr)
                io.stop()
                exit(4)
            }
            fputs("stage=ready-2\n", stderr)
            fputs("stage=stop-2\n", stderr)
            io.stop()
            fputs("stage=stopped-2\n", stderr)

            uplinkLock.lock()
            let frames = uplinkFrames
            uplinkLock.unlock()
            print("PASS CoreAudio no-call start/ready/stop/restart/stop uplink_frames=\(frames)")
        } catch {
            fputs("FAIL CoreAudio lifecycle: \(error.localizedDescription)\n", stderr)
            io.stop()
            exit(1)
        }
    }
}

import Foundation
import Darwin

func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
    if !condition() {
        FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
        exit(1)
    }
}

func testCodecAndIncrementalParser() throws {
    let nonce = Data((0..<AgentAudioProtocol.nonceBytes).map(UInt8.init))
    let payload = Data(repeating: 0x5a, count: AgentAudioProtocol.pcmFrameBytes)
    let frame = AgentAudioFrame(type: .downlink, sequence: 42, nonce: nonce, payload: payload)
    let encoded = try AgentAudioCodec.encode(frame)
    expect(encoded.count == AgentAudioProtocol.headerBytes + AgentAudioProtocol.pcmFrameBytes, "encoded size")

    var parser = AgentAudioParser()
    let firstPart = try parser.append(encoded.prefix(7))
    expect(firstPart.isEmpty, "half header emitted")
    let middlePart = try parser.append(encoded.dropFirst(7).prefix(100))
    expect(middlePart.isEmpty, "half payload emitted")
    let decoded = try parser.append(encoded.dropFirst(107))
    expect(decoded == [frame], "split frame decode")

    let second = AgentAudioFrame(type: .stop, sequence: 43, nonce: nonce, payload: Data())
    let sticky = try AgentAudioCodec.encode(frame) + AgentAudioCodec.encode(second)
    var stickyParser = AgentAudioParser()
    let stickyFrames = try stickyParser.append(sticky)
    expect(stickyFrames == [frame, second], "sticky frames")
    print("PASS codec/parser: binary round-trip, half packets and sticky packets")
}

func testLimits() throws {
    let nonce = Data(repeating: 1, count: AgentAudioProtocol.nonceBytes)
    do {
        _ = try AgentAudioCodec.encode(AgentAudioFrame(type: .downlink, sequence: 1, nonce: nonce,
                                                       payload: Data(count: AgentAudioProtocol.pcmFrameBytes - 1)))
        preconditionFailure("accepted short PCM")
    } catch AgentAudioError.invalidPayloadSize { }
    do {
        var bytes = try AgentAudioCodec.encode(AgentAudioFrame(type: .stop, sequence: 1, nonce: nonce, payload: Data()))
        bytes[12] = 0x7f
        var parser = AgentAudioParser()
        _ = try parser.append(bytes)
        preconditionFailure("accepted oversized payload declaration")
    } catch AgentAudioError.payloadTooLarge { }
    print("PASS limits: fixed 320-byte PCM and bounded payload declarations")
}

func testRingBuffer() {
    var newest = PCMFrameRingBuffer(capacity: 2, overflow: .dropOldest)
    newest.push(Data(repeating: 1, count: 320)); newest.push(Data(repeating: 2, count: 320)); newest.push(Data(repeating: 3, count: 320))
    expect(newest.pop() == Data(repeating: 2, count: 320), "drop oldest")
    expect(newest.pop() == Data(repeating: 3, count: 320), "newest retained")
    expect(newest.popOrSilence() == Data(count: 320), "underrun silence")

    var oldest = PCMFrameRingBuffer(capacity: 1, overflow: .dropNewest)
    oldest.push(Data(repeating: 4, count: 320)); oldest.push(Data(repeating: 5, count: 320))
    expect(oldest.pop() == Data(repeating: 4, count: 320), "drop newest")
    expect(oldest.droppedFrames == 1 && newest.droppedFrames == 1 && newest.underruns == 1, "counters")
    print("PASS ring: bounded overflow policies, counters and underrun silence")
}

func testAudioOwnershipMutualExclusion() {
    expect(AgentAudioOwnership.shared.acquire(.human), "human owner acquire")
    expect(!AgentAudioOwnership.shared.acquire(.agent), "agent must not overlap human")
    AgentAudioOwnership.shared.release(.human)
    expect(AgentAudioOwnership.shared.acquire(.agent), "agent owner acquire")
    expect(!AgentAudioOwnership.shared.acquire(.agent), "same owner must not overlap another session")
    expect(!AgentAudioOwnership.shared.acquire(.human), "human must not overlap agent")
    AgentAudioOwnership.shared.release(.agent)
    print("PASS ownership: human and Agent audio bridges are mutually exclusive")
}

final class FakeModuleIO: AgentModuleAudioIO {
    var onReady: (() -> Void)?
    var onUnavailable: (() -> Void)?
    var onCapturePCM: ((Data) -> Void)?
    var downlink: (() -> Data)?
    var uplink: ((Data) -> Void)?
    var starts = 0
    var stops = 0
    func start(pullDownlink: @escaping () -> Data, pushUplink: @escaping (Data) -> Void) throws {
        starts += 1; downlink = pullDownlink; uplink = pushUplink
    }
    func stop() { stops += 1; downlink = nil; uplink = nil }
}

final class ReadyThenFailModuleIO: AgentModuleAudioIO {
    var onReady: (() -> Void)?
    var onUnavailable: (() -> Void)?
    var onCapturePCM: ((Data) -> Void)?
    func start(pullDownlink: @escaping () -> Data, pushUplink: @escaping (Data) -> Void) throws {
        onReady?()
        throw AgentAudioError.socketFailure("second endpoint failed")
    }
    func stop() {}
}

final class BlockingFirstStartModuleIO: AgentModuleAudioIO {
    var onReady: (() -> Void)?
    var onUnavailable: (() -> Void)?
    var onCapturePCM: ((Data) -> Void)?
    let firstStartEntered = DispatchSemaphore(value: 0)
    let releaseFirstStart = DispatchSemaphore(value: 0)
    private let lock = NSLock()
    private var startCount = 0
    private var stopCount = 0

    var starts: Int { lock.withLock { startCount } }
    var stops: Int { lock.withLock { stopCount } }

    func start(pullDownlink: @escaping () -> Data, pushUplink: @escaping (Data) -> Void) throws {
        let number = lock.withLock { () -> Int in
            startCount += 1
            return startCount
        }
        if number == 1 {
            firstStartEntered.signal()
            _ = releaseFirstStart.wait(timeout: .now() + 3)
        }
    }

    func stop() { lock.withLock { stopCount += 1 } }
}

final class BlockingFirstStopModuleIO: AgentModuleAudioIO {
    var onReady: (() -> Void)?
    var onUnavailable: (() -> Void)?
    var onCapturePCM: ((Data) -> Void)?
    let firstStopEntered = DispatchSemaphore(value: 0)
    let releaseFirstStop = DispatchSemaphore(value: 0)
    private let lock = NSLock()
    private var startCount = 0
    private var stopCount = 0

    var starts: Int { lock.withLock { startCount } }

    func start(pullDownlink: @escaping () -> Data, pushUplink: @escaping (Data) -> Void) throws {
        lock.withLock { startCount += 1 }
    }

    func stop() {
        let number = lock.withLock { () -> Int in
            stopCount += 1
            return stopCount
        }
        if number == 1 {
            firstStopEntered.signal()
            _ = releaseFirstStop.wait(timeout: .now() + 3)
        }
    }
}

func waitUntil(timeout: TimeInterval = 2, _ condition: () -> Bool) -> Bool {
    let deadline = Date().addingTimeInterval(timeout)
    while Date() < deadline {
        if condition() { return true }
        Thread.sleep(forTimeInterval: 0.01)
    }
    return condition()
}

func connectUnix(_ path: String) throws -> Int32 {
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { throw AgentAudioError.socketFailure("socket") }
    var address = sockaddr_un()
    address.sun_family = sa_family_t(AF_UNIX)
    let maxLength = MemoryLayout.size(ofValue: address.sun_path)
    guard path.utf8.count < maxLength else { close(fd); throw AgentAudioError.socketFailure("path") }
    withUnsafeMutableBytes(of: &address.sun_path) { raw in
        raw.initializeMemory(as: UInt8.self, repeating: 0)
        for (index, byte) in path.utf8.enumerated() { raw[index] = byte }
    }
    let result = withUnsafePointer(to: &address) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard result == 0 else { let code = errno; close(fd); throw AgentAudioError.socketFailure("connect \(code)") }
    return fd
}

func writeAll(_ fd: Int32, _ data: Data) {
    data.withUnsafeBytes { raw in
        var offset = 0
        while offset < raw.count {
            let count = Darwin.write(fd, raw.baseAddress!.advanced(by: offset), raw.count - offset)
            precondition(count > 0, "write")
            offset += count
        }
    }
}

func readFrame(_ fd: Int32, parser: inout AgentAudioParser) throws -> AgentAudioFrame {
    var bytes = [UInt8](repeating: 0, count: 1024)
    while true {
        let count = Darwin.read(fd, &bytes, bytes.count)
        guard count > 0 else { throw AgentAudioError.disconnected }
        let frames = try parser.append(Data(bytes[0..<count]))
        if let frame = frames.first { return frame }
    }
}

func testServerHandshakeSessionAndCleanup() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("da-\(getpid())")
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    let path = root.appendingPathComponent("pcm.sock").path
    let module = FakeModuleIO()
    let bridge = AgentAudioBridge(socketPath: path, moduleIO: module)
    try bridge.start()
    expect(bridge.snapshot().transportReady, "transport not ready")
    let mode = try FileManager.default.attributesOfItem(atPath: path)[.posixPermissions] as! NSNumber
    expect(mode.intValue == 0o600, "socket mode")

    let first = try connectUnix(path)
    defer { close(first) }
    var parser = AgentAudioParser()
    let zeroNonce = Data(count: AgentAudioProtocol.nonceBytes)
    writeAll(first, try AgentAudioCodec.encode(AgentAudioFrame(type: .hello, sequence: 0, nonce: zeroNonce, payload: Data())))
    let ack = try readFrame(first, parser: &parser)
    expect(ack.type == .ack && ack.nonce != zeroNonce, "HELLO/ACK nonce")
    Thread.sleep(forTimeInterval: 0.05)
    expect(!bridge.snapshot().moduleIOReady, "bridge became ready before adapter callback")
    module.onReady?()
    Thread.sleep(forTimeInterval: 0.02)
    expect(bridge.snapshot().moduleIOReady, "bridge did not publish adapter readiness")

    let second = try connectUnix(path)
    var secondParser = AgentAudioParser()
    let rejection = try readFrame(second, parser: &secondParser)
    expect(rejection.type == .error, "second client not rejected")
    close(second)

    let pcm = Data(repeating: 7, count: 320)
    writeAll(first, try AgentAudioCodec.encode(AgentAudioFrame(type: .downlink, sequence: 1, nonce: ack.nonce, payload: pcm)))
    Thread.sleep(forTimeInterval: 0.05)
    expect(module.downlink?() == pcm, "downlink not delivered")
    module.uplink?(Data(repeating: 8, count: 320))
    let uplink = try readFrame(first, parser: &parser)
    expect(uplink.type == .uplink && uplink.payload == Data(repeating: 8, count: 320), "uplink")

    module.onUnavailable?()
    Thread.sleep(forTimeInterval: 0.05)
    expect(!bridge.snapshot().moduleIOReady, "runtime endpoint loss did not revoke readiness")
    expect(!bridge.snapshot().clientConnected && module.stops > 0, "unavailable cleanup")

    let third = try connectUnix(path)
    var thirdParser = AgentAudioParser()
    writeAll(third, try AgentAudioCodec.encode(AgentAudioFrame(type: .hello, sequence: 0,
        nonce: zeroNonce, payload: Data())))
    let thirdAck = try readFrame(third, parser: &thirdParser)
    let staleNonce = Data(repeating: 9, count: AgentAudioProtocol.nonceBytes)
    writeAll(third, try AgentAudioCodec.encode(AgentAudioFrame(type: .downlink, sequence: 1,
        nonce: staleNonce, payload: pcm)))
    let staleError = try readFrame(third, parser: &thirdParser)
    expect(thirdAck.type == .ack && staleError.type == .error, "stale session not rejected")
    close(third)
    Thread.sleep(forTimeInterval: 0.05)

    bridge.stop()
    expect(!FileManager.default.fileExists(atPath: path), "socket cleanup")
    print("PASS server/bridge: 0600, single client, HELLO/ACK nonce, PCM duplex, stale-session rejection, runtime-unavailable and cleanup")
}

func testReadyThenStartFailureDoesNotStick() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("da-fail-\(getpid())")
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    let bridge = AgentAudioBridge(socketPath: root.appendingPathComponent("pcm.sock").path,
                                  moduleIO: ReadyThenFailModuleIO())
    try bridge.start()
    defer { bridge.stop() }
    let fd = try connectUnix(bridge.socketPath)
    defer { close(fd) }
    var parser = AgentAudioParser()
    let hello = AgentAudioFrame(type: .hello, sequence: 0,
                                nonce: Data(count: AgentAudioProtocol.nonceBytes), payload: Data())
    writeAll(fd, try AgentAudioCodec.encode(hello))
    _ = try readFrame(fd, parser: &parser)
    Thread.sleep(forTimeInterval: 0.05)
    expect(!bridge.snapshot().moduleIOReady, "failed second endpoint left bridge ready")
    print("PASS bridge readiness is revoked when module start fails")
}

func testStaleStartCannotResurrectOrStopNewSession() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("da-race-\(getpid())")
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    let module = BlockingFirstStartModuleIO()
    let bridge = AgentAudioBridge(socketPath: root.appendingPathComponent("pcm.sock").path, moduleIO: module)
    try bridge.start()
    defer { bridge.stop() }

    let zeroNonce = Data(count: AgentAudioProtocol.nonceBytes)
    let hello = try AgentAudioCodec.encode(AgentAudioFrame(type: .hello, sequence: 0,
        nonce: zeroNonce, payload: Data()))
    let first = try connectUnix(bridge.socketPath)
    var firstParser = AgentAudioParser()
    writeAll(first, hello)
    _ = try readFrame(first, parser: &firstParser)
    expect(module.firstStartEntered.wait(timeout: .now() + 2) == .success, "first module start did not block")

    module.onUnavailable?()
    let second = try connectUnix(bridge.socketPath)
    defer { close(second) }
    var secondParser = AgentAudioParser()
    writeAll(second, hello)
    _ = try readFrame(second, parser: &secondParser)
    module.releaseFirstStart.signal()

    expect(waitUntil { module.starts == 2 && bridge.snapshot().clientConnected },
           "new session did not start after stale startup invalidation")
    module.onReady?()
    expect(waitUntil { bridge.snapshot().moduleIOReady }, "new session did not become ready")
    let stopsAfterNewStart = module.stops
    Thread.sleep(forTimeInterval: 0.1)
    expect(module.stops == stopsAfterNewStart, "stale asynchronous stop tore down the new session")
    expect(bridge.snapshot().clientConnected && bridge.snapshot().moduleIOReady,
           "stale startup resurrected or corrupted bridge state")
    close(first)
    print("PASS bridge generation serialization blocks stale start/stop races")
}

func testClientDuringOldStopGetsRetryError() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("da-stop-race-\(getpid())")
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    let module = BlockingFirstStopModuleIO()
    let bridge = AgentAudioBridge(socketPath: root.appendingPathComponent("pcm.sock").path, moduleIO: module)
    try bridge.start()
    defer {
        module.releaseFirstStop.signal()
        bridge.stop()
    }

    let zeroNonce = Data(count: AgentAudioProtocol.nonceBytes)
    let hello = try AgentAudioCodec.encode(AgentAudioFrame(type: .hello, sequence: 0,
        nonce: zeroNonce, payload: Data()))
    let first = try connectUnix(bridge.socketPath)
    var firstParser = AgentAudioParser()
    writeAll(first, hello)
    _ = try readFrame(first, parser: &firstParser)
    expect(waitUntil { bridge.snapshot().clientConnected }, "first session did not connect")
    close(first)
    expect(module.firstStopEntered.wait(timeout: .now() + 2) == .success, "old session stop did not block")

    let second = try connectUnix(bridge.socketPath)
    var secondParser = AgentAudioParser()
    writeAll(second, hello)
    let secondAck = try readFrame(second, parser: &secondParser)
    let busy = try readFrame(second, parser: &secondParser)
    expect(secondAck.type == .ack && busy.type == .error && String(data: busy.payload, encoding: .utf8) == "module_audio_busy_retry",
           "client racing old stop must receive retryable busy error")
    close(second)

    module.releaseFirstStop.signal()
    expect(waitUntil { !bridge.snapshot().clientConnected }, "old stop did not finish")
    let third = try connectUnix(bridge.socketPath)
    var thirdParser = AgentAudioParser()
    writeAll(third, hello)
    _ = try readFrame(third, parser: &thirdParser)
    expect(waitUntil { module.starts == 2 && bridge.snapshot().clientConnected },
           "retry after old stop did not establish a new session")
    close(third)
    print("PASS client racing old stop receives retry error and can reconnect")
}

try testCodecAndIncrementalParser()
try testLimits()
testRingBuffer()
testAudioOwnershipMutualExclusion()
try testServerHandshakeSessionAndCleanup()
try testReadyThenStartFailureDoesNotStick()
try testStaleStartCannotResurrectOrStopNewSession()
try testClientDuringOldStopGetsRetryError()
print("PASS Agent PCM format: PCM16LE 8000 Hz mono, 20 ms / 320 bytes")

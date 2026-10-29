import Foundation
import Darwin

/// Phase-1 Agent audio wire format. Audio payloads are always PCM16LE,
/// 8 kHz, mono, 20 ms (160 samples / 320 bytes).
enum AgentAudioProtocol {
    static let magic = Data([0x44, 0x4a, 0x41, 0x31]) // "DJA1"
    static let version: UInt8 = 1
    static let headerBytes = 32
    static let nonceBytes = 16
    static let pcmFrameBytes = 320
    static let maximumPayloadBytes = 4096
    static let sampleRate = 8_000
    static let channels = 1
    static let frameMilliseconds = 20
}

enum AgentAudioFrameType: UInt8, Equatable {
    case hello = 1
    case ack = 2
    case downlink = 3
    case uplink = 4
    case stop = 5
    case error = 6
}

struct AgentAudioFrame: Equatable {
    let type: AgentAudioFrameType
    let sequence: UInt32
    let nonce: Data
    let payload: Data
}

enum AgentAudioError: Error, Equatable {
    case invalidMagic
    case unsupportedVersion
    case unknownFrameType
    case invalidNonceSize
    case invalidPayloadSize
    case payloadTooLarge
    case invalidHandshake
    case staleSession
    case invalidSequence
    case disconnected
    case socketFailure(String)
}

enum AgentAudioCodec {
    static func encode(_ frame: AgentAudioFrame) throws -> Data {
        guard frame.nonce.count == AgentAudioProtocol.nonceBytes else { throw AgentAudioError.invalidNonceSize }
        guard frame.payload.count <= AgentAudioProtocol.maximumPayloadBytes else { throw AgentAudioError.payloadTooLarge }
        if frame.type == .downlink || frame.type == .uplink {
            guard frame.payload.count == AgentAudioProtocol.pcmFrameBytes else { throw AgentAudioError.invalidPayloadSize }
        }
        if frame.type == .hello || frame.type == .ack || frame.type == .stop {
            guard frame.payload.isEmpty else { throw AgentAudioError.invalidPayloadSize }
        }
        var data = Data()
        data.reserveCapacity(AgentAudioProtocol.headerBytes + frame.payload.count)
        data.append(AgentAudioProtocol.magic)
        data.append(AgentAudioProtocol.version)
        data.append(frame.type.rawValue)
        data.append(contentsOf: [0, 0])
        appendBE(frame.sequence, to: &data)
        appendBE(UInt32(frame.payload.count), to: &data)
        data.append(frame.nonce)
        data.append(frame.payload)
        return data
    }

    static func decode(header: Data, payload: Data) throws -> AgentAudioFrame {
        guard header.count == AgentAudioProtocol.headerBytes else { throw AgentAudioError.invalidPayloadSize }
        guard header.prefix(4) == AgentAudioProtocol.magic else { throw AgentAudioError.invalidMagic }
        guard header[4] == AgentAudioProtocol.version else { throw AgentAudioError.unsupportedVersion }
        guard let type = AgentAudioFrameType(rawValue: header[5]) else { throw AgentAudioError.unknownFrameType }
        let sequence = readBE(header, at: 8)
        let declared = Int(readBE(header, at: 12))
        guard declared <= AgentAudioProtocol.maximumPayloadBytes else { throw AgentAudioError.payloadTooLarge }
        guard declared == payload.count else { throw AgentAudioError.invalidPayloadSize }
        return try validated(AgentAudioFrame(type: type, sequence: sequence,
                                             nonce: Data(header[16..<32]), payload: payload))
    }

    private static func validated(_ frame: AgentAudioFrame) throws -> AgentAudioFrame {
        _ = try encode(frame)
        return frame
    }

    private static func appendBE(_ value: UInt32, to data: inout Data) {
        data.append(UInt8((value >> 24) & 0xff))
        data.append(UInt8((value >> 16) & 0xff))
        data.append(UInt8((value >> 8) & 0xff))
        data.append(UInt8(value & 0xff))
    }

    static func readBE(_ data: Data, at offset: Int) -> UInt32 {
        let start = data.index(data.startIndex, offsetBy: offset)
        let b0 = data[start]
        let b1 = data[data.index(after: start)]
        let b2 = data[data.index(start, offsetBy: 2)]
        let b3 = data[data.index(start, offsetBy: 3)]
        return (UInt32(b0) << 24) | (UInt32(b1) << 16) | (UInt32(b2) << 8) | UInt32(b3)
    }
}

struct AgentAudioParser {
    private var buffer = Data()

    mutating func append<D: DataProtocol>(_ bytes: D) throws -> [AgentAudioFrame] {
        buffer.append(contentsOf: bytes)
        var frames: [AgentAudioFrame] = []
        while buffer.count >= AgentAudioProtocol.headerBytes {
            guard buffer.prefix(4) == AgentAudioProtocol.magic else { throw AgentAudioError.invalidMagic }
            let payloadLength = Int(AgentAudioCodec.readBE(buffer, at: 12))
            guard payloadLength <= AgentAudioProtocol.maximumPayloadBytes else { throw AgentAudioError.payloadTooLarge }
            let total = AgentAudioProtocol.headerBytes + payloadLength
            guard buffer.count >= total else { break }
            let header = Data(buffer.prefix(AgentAudioProtocol.headerBytes))
            let payload = Data(buffer.dropFirst(AgentAudioProtocol.headerBytes).prefix(payloadLength))
            frames.append(try AgentAudioCodec.decode(header: header, payload: payload))
            buffer = Data(buffer.dropFirst(total))
        }
        return frames
    }

    mutating func reset() { buffer.removeAll(keepingCapacity: false) }
}

enum PCMOverflowPolicy { case dropOldest, dropNewest }

struct PCMFrameRingBuffer {
    private(set) var droppedFrames = 0
    private(set) var underruns = 0
    private var frames: [Data] = []
    let capacity: Int
    let overflow: PCMOverflowPolicy

    init(capacity: Int, overflow: PCMOverflowPolicy) {
        precondition(capacity > 0)
        self.capacity = capacity
        self.overflow = overflow
    }

    mutating func push(_ frame: Data) {
        guard frame.count == AgentAudioProtocol.pcmFrameBytes else { return }
        if frames.count == capacity {
            droppedFrames += 1
            if overflow == .dropNewest { return }
            frames.removeFirst()
        }
        frames.append(frame)
    }

    mutating func pop() -> Data? {
        guard !frames.isEmpty else { return nil }
        return frames.removeFirst()
    }

    mutating func popOrSilence() -> Data {
        if let frame = pop() { return frame }
        underruns += 1
        return Data(count: AgentAudioProtocol.pcmFrameBytes)
    }

    mutating func removeAll() { frames.removeAll(keepingCapacity: true) }
}

protocol AgentModuleAudioIO: AnyObject {
    var onReady: (() -> Void)? { get set }
    var onUnavailable: (() -> Void)? { get set }
    var onCapturePCM: ((Data) -> Void)? { get set }
    func start(pullDownlink: @escaping () -> Data, pushUplink: @escaping (Data) -> Void) throws
    func stop()
}

enum AgentAudioOwner: Equatable { case human, agent }

final class AgentAudioOwnership: @unchecked Sendable {
    static let shared = AgentAudioOwnership()
    private let lock = NSLock()
    private var owner: AgentAudioOwner?

    func acquire(_ requested: AgentAudioOwner) -> Bool {
        lock.withLock {
            guard owner == nil else { return false }
            owner = requested
            return true
        }
    }

    func release(_ released: AgentAudioOwner) {
        lock.withLock { if owner == released { owner = nil } }
    }

    func snapshot() -> AgentAudioOwner? { lock.withLock { owner } }
}

/// Placeholder used until the CoreAudio UAC adapter is wired on a machine with
/// the module. The transport is independently usable and testable.
final class UnavailableAgentModuleAudioIO: AgentModuleAudioIO {
    var onReady: (() -> Void)?
    var onUnavailable: (() -> Void)?
    var onCapturePCM: ((Data) -> Void)?
    func start(pullDownlink: @escaping () -> Data, pushUplink: @escaping (Data) -> Void) throws {
        throw AgentAudioError.socketFailure("Agent CoreAudio module IO is not wired")
    }
    func stop() {}
}

struct AgentAudioBridgeState: Equatable {
    var transportReady = false
    var moduleIOReady = false
    var clientConnected = false
    var asrCompiled = false
    var asrRunning = false
    var asrError: String?
    var sessionNonce: Data?
    var lastError: String?

    var asrAvailable: Bool {
        asrCompiled && asrRunning && asrError == nil
    }
}

final class AgentAudioBridge: @unchecked Sendable {
    static let shared: AgentAudioBridge = {
        let socketPath = FlameRuntimePaths.prepareSupportDirectory()
            .appendingPathComponent(FlameRuntimePaths.agentAudioSocketName).path
        return AgentAudioBridge(
            socketPath: socketPath,
            moduleIO: CoreAudioAgentModuleAudioIO())
    }()

    let socketPath: String
    var onStateChange: ((AgentAudioBridgeState) -> Void)?
    var onNativeTranscript: ((String, Bool) -> Void)?
    private let moduleIO: AgentModuleAudioIO
    private let lock = NSLock()
    private let moduleIOLifecycleLock = NSLock()
    private var state = AgentAudioBridgeState()
    private var downlink = PCMFrameRingBuffer(capacity: 25, overflow: .dropOldest)
    private var server: AgentAudioUnixServer?
    private var moduleIOGeneration: UInt64 = 0
    private var nativeTranscriber: AnyObject?

    init(socketPath: String, moduleIO: AgentModuleAudioIO) {
        self.socketPath = socketPath
        self.moduleIO = moduleIO
    }

    func start() throws {
        let server = AgentAudioUnixServer(path: socketPath)
        server.onAuthenticated = { [weak self] nonce in self?.clientAuthenticated(nonce) }
        server.onFrame = { [weak self] frame in self?.receive(frame) }
        server.onDisconnect = { [weak self] nonce in self?.disconnected(nonce: nonce) }
        try server.start()
        lock.withLock {
            self.server = server
            state.transportReady = true
            if #available(macOS 27.0, *) {
                state.asrCompiled = true
            }
            state.lastError = nil
        }
        publishState()
    }

    func stop() {
        let values = lock.withLock { () -> (AgentAudioUnixServer?, UInt64) in
            moduleIOGeneration &+= 1
            let stopGeneration = moduleIOGeneration
            state = AgentAudioBridgeState()
            downlink.removeAll()
            let value = server
            server = nil
            return (value, stopGeneration)
        }
        stopModuleIO(ifCurrentGeneration: values.1)
        AgentAudioOwnership.shared.release(.agent)
        values.0?.stop()
        publishState()
    }

    func snapshot() -> AgentAudioBridgeState { lock.withLock { state } }

    private func clientAuthenticated(_ nonce: Data) {
        var generation: UInt64 = 0
        do {
            guard AgentAudioOwnership.shared.acquire(.agent) else {
                server?.failCurrentClient("module_audio_busy_retry")
                return
            }
            generation = lock.withLock {
                moduleIOGeneration &+= 1
                state.moduleIOReady = false
                state.clientConnected = false
                state.sessionNonce = nonce
                return moduleIOGeneration
            }
            moduleIO.onReady = { [weak self] in
                guard let self else { return }
                let changed = self.lock.withLock { () -> Bool in
                    guard self.moduleIOGeneration == generation else { return false }
                    self.state.moduleIOReady = true
                    return true
                }
                if changed { self.publishState() }
            }
            moduleIO.onUnavailable = { [weak self] in self?.moduleUnavailable(generation: generation) }
            moduleIO.onCapturePCM = { [weak self] pcm in
                guard #available(macOS 27.0, *), let self else { return }
                let transcriber = self.lock.withLock { self.nativeTranscriber as? NativeSpeechTranscriber }
                transcriber?.appendPCM16LE(pcm)
            }
            startNativeTranscriber(generation: generation)
            try startModuleIO(generation: generation, pullDownlink: { [weak self] in
                self?.lock.withLock { self?.downlink.popOrSilence() } ?? Data(count: AgentAudioProtocol.pcmFrameBytes)
            }, pushUplink: { [weak self] pcm in
                guard pcm.count == AgentAudioProtocol.pcmFrameBytes else { return }
                self?.lock.withLock { self?.server }?.sendUplink(pcm)
            })
            let committed = lock.withLock { () -> Bool in
                guard moduleIOGeneration == generation else { return false }
                state.clientConnected = true
                state.sessionNonce = nonce
                state.lastError = nil
                return true
            }
            guard committed else { throw AgentAudioError.disconnected }
            publishState()
        } catch {
            let stopGeneration = lock.withLock { () -> UInt64? in
                guard moduleIOGeneration == generation else { return nil }
                moduleIOGeneration &+= 1
                state.moduleIOReady = false
                state.clientConnected = false
                state.sessionNonce = nil
                state.lastError = String(describing: error)
                return moduleIOGeneration
            }
            if let stopGeneration {
                stopModuleIO(ifCurrentGeneration: stopGeneration)
                AgentAudioOwnership.shared.release(.agent)
                publishState()
                server?.failCurrentClient("module_io_unavailable")
            }
        }
    }

    private func receive(_ frame: AgentAudioFrame) {
        switch frame.type {
        case .downlink:
            lock.withLock { downlink.push(frame.payload) }
        case .stop:
            server?.closeCurrentClient()
        default:
            break
        }
    }

    private func disconnected(nonce: Data?) {
        let stopGeneration = lock.withLock { () -> UInt64? in
            guard let nonce, state.sessionNonce == nonce else { return nil }
            moduleIOGeneration &+= 1
            downlink.removeAll()
            state.moduleIOReady = false
            state.clientConnected = false
            state.sessionNonce = nil
            return moduleIOGeneration
        }
        guard let stopGeneration else { return }
        stopModuleIO(ifCurrentGeneration: stopGeneration)
        AgentAudioOwnership.shared.release(.agent)
        publishState()
    }

    private func moduleUnavailable(generation: UInt64) {
        let values = lock.withLock { () -> (AgentAudioUnixServer, UInt64)? in
            guard moduleIOGeneration == generation, let server else { return nil }
            moduleIOGeneration &+= 1
            let stopGeneration = moduleIOGeneration
            downlink.removeAll()
            state.moduleIOReady = false
            state.clientConnected = false
            state.sessionNonce = nil
            state.lastError = "module_io_unavailable"
            return (server, stopGeneration)
        }
        guard let values else { return }
        AgentAudioOwnership.shared.release(.agent)
        publishState()
        values.0.failCurrentClient("module_io_unavailable")
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            self?.stopModuleIO(ifCurrentGeneration: values.1)
        }
    }

    private func startModuleIO(generation: UInt64, pullDownlink: @escaping () -> Data,
                               pushUplink: @escaping (Data) -> Void) throws {
        moduleIOLifecycleLock.lock()
        defer { moduleIOLifecycleLock.unlock() }
        guard lock.withLock({ moduleIOGeneration == generation }) else { throw AgentAudioError.disconnected }
        try moduleIO.start(pullDownlink: pullDownlink, pushUplink: pushUplink)
        guard lock.withLock({ moduleIOGeneration == generation }) else {
            moduleIO.stop()
            throw AgentAudioError.disconnected
        }
    }

    private func stopModuleIO(ifCurrentGeneration generation: UInt64) {
        moduleIOLifecycleLock.lock()
        defer { moduleIOLifecycleLock.unlock() }
        guard lock.withLock({ moduleIOGeneration == generation }) else { return }
        moduleIO.stop()
        stopNativeTranscriber()
    }

    private struct NativeTranscriptSession: Decodable {
        let captureSessionID: String?

        enum CodingKeys: String, CodingKey {
            case captureSessionID = "capture_session_id"
        }
    }

    private func startNativeTranscriber(generation: UInt64) {
        guard #available(macOS 27.0, *) else { return }
        Task { [weak self] in
            guard let self,
                  let session: NativeTranscriptSession = try? await APIClient(timeoutInterval: 5).get("api/agent/audio/status"),
                  let sessionID = session.captureSessionID?.trimmingCharacters(in: .whitespacesAndNewlines),
                  !sessionID.isEmpty else { return }
            let transcriber = NativeSpeechTranscriber(
                onResult: { [weak self] text, isFinal in
                    self?.onNativeTranscript?(text, isFinal)
                    Task { await Self.reportNativeTranscript(text: text, isFinal: isFinal, sessionID: sessionID) }
                },
                onError: { [weak self] message in
                    self?.nativeTranscriberFailed(generation: generation, message: message)
                })
            guard self.lock.withLock({ self.moduleIOGeneration == generation }) else { return }
            do {
                try await transcriber.start()
            } catch {
                let changed = self.lock.withLock { () -> Bool in
                    guard self.moduleIOGeneration == generation else { return false }
                    self.state.asrRunning = false
                    self.state.asrError = error.localizedDescription
                    return true
                }
                if changed { self.publishState() }
                return
            }
            let installed = self.lock.withLock { () -> Bool in
                guard self.moduleIOGeneration == generation, self.nativeTranscriber == nil else { return false }
                self.nativeTranscriber = transcriber
                self.state.asrRunning = true
                self.state.asrError = nil
                return true
            }
            if !installed {
                await transcriber.stop()
            } else {
                self.publishState()
            }
        }
    }

    private func stopNativeTranscriber() {
        guard #available(macOS 27.0, *) else { return }
        let transcriber = lock.withLock { () -> NativeSpeechTranscriber? in
            let value = nativeTranscriber as? NativeSpeechTranscriber
            nativeTranscriber = nil
            state.asrRunning = false
            return value
        }
        Task { await transcriber?.stop() }
        publishState()
    }

    private func nativeTranscriberFailed(generation: UInt64, message: String) {
        let changed = lock.withLock { () -> Bool in
            guard moduleIOGeneration == generation else { return false }
            state.asrRunning = false
            state.asrError = message
            nativeTranscriber = nil
            return true
        }
        if changed { publishState() }
    }

    private struct NativeTranscriptUpdate: Encodable {
        let text: String
        let isFinal: Bool
        let sessionID: String

        enum CodingKeys: String, CodingKey {
            case text
            case isFinal = "final"
            case sessionID = "session_id"
        }
    }

    private static func reportNativeTranscript(text: String, isFinal: Bool, sessionID: String) async {
        let update = NativeTranscriptUpdate(text: text, isFinal: isFinal, sessionID: sessionID)
        do {
            try await APIClient(timeoutInterval: 5).send(
                "api/agent/audio/transcript", method: "PUT", body: update)
        } catch {
            // 转录结果不应阻塞实时音频线程；后端不可用时丢弃本次结果。
        }
    }

    private func publishState() {
        let value = snapshot()
        onStateChange?(value)
    }
}

final class AgentAudioUnixServer: @unchecked Sendable {
    let path: String
    var onAuthenticated: ((Data) -> Void)?
    var onFrame: ((AgentAudioFrame) -> Void)?
    var onDisconnect: ((Data?) -> Void)?

    private let stateQueue = DispatchQueue(label: "com.djonehub.agent-audio.server")
    private let writeLock = NSLock()
    private var listenFD: Int32 = -1
    private var clientFD: Int32 = -1
    private var sessionNonce: Data?
    private var expectedSequence: UInt32 = 1
    private var uplinkSequence: UInt32 = 1
    private var running = false

    init(path: String) { self.path = path }

    func start() throws {
        try FileManager.default.createDirectory(at: URL(fileURLWithPath: path).deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        try? FileManager.default.removeItem(atPath: path)
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw AgentAudioError.socketFailure("socket errno=\(errno)") }
        var noSigPipe: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &noSigPipe, socklen_t(MemoryLayout<Int32>.size))
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        guard path.utf8.count < MemoryLayout.size(ofValue: address.sun_path) else {
            close(fd); throw AgentAudioError.socketFailure("socket path too long")
        }
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.initializeMemory(as: UInt8.self, repeating: 0)
            for (index, byte) in path.utf8.enumerated() { raw[index] = byte }
        }
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bound == 0, chmod(path, 0o600) == 0, Darwin.listen(fd, 4) == 0 else {
            let code = errno; close(fd); try? FileManager.default.removeItem(atPath: path)
            throw AgentAudioError.socketFailure("bind/listen errno=\(code)")
        }
        stateQueue.sync { listenFD = fd; running = true }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in self?.acceptLoop() }
    }

    func stop() {
        let descriptors = stateQueue.sync { () -> (Int32, Int32) in
            running = false
            let values = (listenFD, clientFD)
            listenFD = -1; clientFD = -1; sessionNonce = nil
            return values
        }
        if descriptors.1 >= 0 { shutdown(descriptors.1, SHUT_RDWR); close(descriptors.1) }
        if descriptors.0 >= 0 { shutdown(descriptors.0, SHUT_RDWR); close(descriptors.0) }
        try? FileManager.default.removeItem(atPath: path)
    }

    func sendUplink(_ pcm: Data) {
        let tuple = stateQueue.sync { () -> (Int32, Data?, UInt32) in
            let sequence = uplinkSequence
            uplinkSequence &+= 1
            return (clientFD, sessionNonce, sequence)
        }
        guard tuple.0 >= 0, let nonce = tuple.1 else { return }
        try? send(AgentAudioFrame(type: .uplink, sequence: tuple.2, nonce: nonce, payload: pcm), to: tuple.0)
    }

    func failCurrentClient(_ message: String) {
        let tuple = stateQueue.sync { (clientFD, sessionNonce) }
        guard tuple.0 >= 0, let nonce = tuple.1 else { return }
        try? send(AgentAudioFrame(type: .error, sequence: 0, nonce: nonce,
                                  payload: Data(message.utf8)), to: tuple.0)
        closeCurrentClient()
    }

    func closeCurrentClient() {
        let fd = stateQueue.sync { () -> Int32 in
            let value = clientFD
            clientFD = -1; sessionNonce = nil
            return value
        }
        if fd >= 0 { shutdown(fd, SHUT_RDWR); close(fd) }
    }

    private func acceptLoop() {
        while stateQueue.sync(execute: { running }) {
            let listener = stateQueue.sync { listenFD }
            guard listener >= 0 else { return }
            let fd = Darwin.accept(listener, nil, nil)
            if fd < 0 { if stateQueue.sync(execute: { running }) { continue }; return }
            var noSigPipe: Int32 = 1
            setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &noSigPipe, socklen_t(MemoryLayout<Int32>.size))
            let accepted = stateQueue.sync { () -> Bool in
                guard clientFD < 0 else { return false }
                clientFD = fd; sessionNonce = nil; expectedSequence = 1; uplinkSequence = 1
                return true
            }
            if !accepted {
                let nonce = Data(count: AgentAudioProtocol.nonceBytes)
                try? send(AgentAudioFrame(type: .error, sequence: 0, nonce: nonce,
                                          payload: Data("client_already_connected".utf8)), to: fd)
                close(fd)
                continue
            }
            DispatchQueue.global(qos: .userInitiated).async { [weak self] in
                self?.handleClient(fd)
            }
        }
    }

    private func handleClient(_ fd: Int32) {
        var parser = AgentAudioParser()
        var bytes = [UInt8](repeating: 0, count: 4096)
        defer {
            let disconnected = stateQueue.sync { () -> (owned: Bool, nonce: Data?) in
                guard clientFD == fd else { return (false, nil) }
                let nonce = sessionNonce
                clientFD = -1; sessionNonce = nil
                return (true, nonce)
            }
            if disconnected.owned {
                close(fd)
                onDisconnect?(disconnected.nonce)
            }
        }
        while stateQueue.sync(execute: { running && clientFD == fd }) {
            let count = Darwin.read(fd, &bytes, bytes.count)
            guard count > 0 else { return }
            do {
                for frame in try parser.append(Data(bytes[0..<count])) {
                    try process(frame, fd: fd)
                }
            } catch {
                let nonce = stateQueue.sync { sessionNonce } ?? Data(count: AgentAudioProtocol.nonceBytes)
                try? send(AgentAudioFrame(type: .error, sequence: 0, nonce: nonce,
                                          payload: Data(String(describing: error).utf8)), to: fd)
                return
            }
        }
    }

    private func process(_ frame: AgentAudioFrame, fd: Int32) throws {
        let nonce = stateQueue.sync { sessionNonce }
        if nonce == nil {
            guard frame.type == .hello, frame.sequence == 0, frame.payload.isEmpty,
                  frame.nonce == Data(count: AgentAudioProtocol.nonceBytes) else {
                throw AgentAudioError.invalidHandshake
            }
            let generated = randomNonce()
            stateQueue.sync { sessionNonce = generated }
            try send(AgentAudioFrame(type: .ack, sequence: 0, nonce: generated, payload: Data()), to: fd)
            onAuthenticated?(generated)
            return
        }
        guard frame.nonce == nonce else { throw AgentAudioError.staleSession }
        guard frame.sequence == stateQueue.sync(execute: { expectedSequence }) else {
            throw AgentAudioError.invalidSequence
        }
        stateQueue.sync { expectedSequence &+= 1 }
        guard frame.type == .downlink || frame.type == .stop else { throw AgentAudioError.invalidHandshake }
        onFrame?(frame)
    }

    private func send(_ frame: AgentAudioFrame, to fd: Int32) throws {
        let data = try AgentAudioCodec.encode(frame)
        try writeLock.withLock {
            try data.withUnsafeBytes { raw in
                var offset = 0
                while offset < raw.count {
                    let count = Darwin.write(fd, raw.baseAddress!.advanced(by: offset), raw.count - offset)
                    guard count > 0 else { throw AgentAudioError.disconnected }
                    offset += count
                }
            }
        }
    }

    private func randomNonce() -> Data {
        var bytes = [UInt8](repeating: 0, count: AgentAudioProtocol.nonceBytes)
        arc4random_buf(&bytes, bytes.count)
        return Data(bytes)
    }
}

private extension NSLock {
    func withLock<T>(_ body: () throws -> T) rethrows -> T {
        lock(); defer { unlock() }
        return try body()
    }
}

import Foundation
#if !FLAME_RUNTIME_MIGRATION_PROBE
import Combine
#endif

enum FlameRuntimePaths {
    static let supportDirectoryName = "FlameDJHub"
    static let legacySupportDirectoryName = "DJOneHubNative"
    static let backendSocketName = "flame-dj-hub.sock"
    static let agentAudioSocketName = "agent-audio.sock"

    static var supportDirectory: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
            .appendingPathComponent(supportDirectoryName, isDirectory: true)
    }

    static func prepareSupportDirectory(fileManager: FileManager = .default) -> URL {
        let destination = supportDirectory
        let legacy = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
            .appendingPathComponent(legacySupportDirectoryName, isDirectory: true)

        do {
            try fileManager.createDirectory(at: destination, withIntermediateDirectories: true)
            try fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: destination.path)
            if legacy != destination, itemExists(at: legacy, fileManager: fileManager) {
                try migrateSupportData(from: legacy, to: destination, fileManager: fileManager)
            }
        } catch {
            let message = "Flame DJ Hub support migration failed: \(error)\n"
            try? FileHandle.standardError.write(contentsOf: Data(message.utf8))
        }
        return destination
    }

    static func migrateSupportData(
        from source: URL,
        to destination: URL,
        fileManager: FileManager = .default
    ) throws {
        let sourceValues = try source.resourceValues(forKeys: [.isSymbolicLinkKey, .isDirectoryKey])
        guard sourceValues.isSymbolicLink != true else { return }
        guard sourceValues.isDirectory == true else {
            throw CocoaError(.fileReadUnsupportedScheme)
        }

        try fileManager.createDirectory(at: destination, withIntermediateDirectories: true)
        try migrateDirectory(from: source, to: destination, fileManager: fileManager)
    }

    private static func migrateDirectory(
        from source: URL,
        to destination: URL,
        fileManager: FileManager
    ) throws {
        let entries = try fileManager.contentsOfDirectory(
            at: source,
            includingPropertiesForKeys: [.isRegularFileKey, .isDirectoryKey, .isSymbolicLinkKey],
            options: []
        )
        for entry in entries {
            let values = try entry.resourceValues(forKeys: [.isRegularFileKey, .isDirectoryKey, .isSymbolicLinkKey])
            if values.isSymbolicLink == true || isTransient(entry.lastPathComponent) {
                continue
            }

            let target = destination.appendingPathComponent(entry.lastPathComponent, isDirectory: values.isDirectory == true)
            if values.isDirectory == true {
                if itemExists(at: target, fileManager: fileManager) {
                    var isDirectory: ObjCBool = false
                    guard fileManager.fileExists(atPath: target.path, isDirectory: &isDirectory),
                          isDirectory.boolValue,
                          (try? target.resourceValues(forKeys: [.isSymbolicLinkKey]).isSymbolicLink) != true else {
                        continue
                    }
                } else {
                    try fileManager.createDirectory(at: target, withIntermediateDirectories: false)
                }
                try migrateDirectory(from: entry, to: target, fileManager: fileManager)
            } else if values.isRegularFile == true,
                      !itemExists(at: target, fileManager: fileManager) {
                try copyFileAtomically(from: entry, to: target, fileManager: fileManager)
            }
        }
    }

    private static func copyFileAtomically(
        from source: URL,
        to destination: URL,
        fileManager: FileManager
    ) throws {
        let temporary = destination.deletingLastPathComponent()
            .appendingPathComponent(".migration-\(UUID().uuidString)")
        var shouldRemoveTemporary = true
        defer {
            if shouldRemoveTemporary {
                try? fileManager.removeItem(at: temporary)
            }
        }

        guard fileManager.createFile(atPath: temporary.path, contents: nil) else {
            throw CocoaError(.fileWriteUnknown)
        }
        if let permissions = try fileManager.attributesOfItem(atPath: source.path)[.posixPermissions] {
            try fileManager.setAttributes([.posixPermissions: permissions], ofItemAtPath: temporary.path)
        }
        let input = try FileHandle(forReadingFrom: source)
        let output = try FileHandle(forWritingTo: temporary)
        do {
            while let data = try input.read(upToCount: 1024 * 1024), !data.isEmpty {
                try output.write(contentsOf: data)
            }
            try output.synchronize()
            try output.close()
            try input.close()
        } catch {
            try? output.close()
            try? input.close()
            throw error
        }

        guard !itemExists(at: destination, fileManager: fileManager) else { return }
        try fileManager.moveItem(at: temporary, to: destination)
        shouldRemoveTemporary = false
    }

    private static func itemExists(at url: URL, fileManager: FileManager) -> Bool {
        if fileManager.fileExists(atPath: url.path) {
            return true
        }
        return (try? fileManager.destinationOfSymbolicLink(atPath: url.path)) != nil
    }

    private static func isTransient(_ name: String) -> Bool {
        let lower = name.lowercased()
        return lower.hasSuffix(".sock") || lower.hasSuffix(".log") ||
            lower.hasSuffix(".pid") || lower.hasSuffix(".tmp")
    }
}

#if !FLAME_RUNTIME_MIGRATION_PROBE
/// 管理 Go 后端子进程的生命周期，并对外暴露 Unix socket 路径。
final class BackendProcess: ObservableObject {
    enum State: Equatable {
        case stopped
        case starting
        case running
        case failed(String)
    }

    static let shared = BackendProcess()

    @Published private(set) var state: State = .stopped
    @Published private(set) var childPID: pid_t = 0

    private var process: Process?
    private var stdoutTask: Task<Void, Never>?
    private var stderrTask: Task<Void, Never>?

    let socketPath: String
    let logURL: URL

    private init() {
        let fm = FileManager.default
        let support = FlameRuntimePaths.prepareSupportDirectory(fileManager: fm)
        socketPath = support.appendingPathComponent(FlameRuntimePaths.backendSocketName).path
        logURL = support.appendingPathComponent("djonehub.log")

        UnixSocketURLProtocol.socketPath = socketPath
    }

    var binaryURL: URL? {
        Bundle.main.url(forResource: "djonehubd", withExtension: nil, subdirectory: "backend")
    }

    func start() {
        if case .running = state, process?.isRunning == true { return }
        guard let binary = binaryURL else {
            state = .failed("缺少后端程序 djonehubd")
            return
        }

        try? FileManager.default.removeItem(atPath: socketPath)

        let proc = Process()
        proc.executableURL = binary
        proc.arguments = ["-listen", "unix:\(socketPath)"]
        var environment = ProcessInfo.processInfo.environment
        environment["DJONEHUB_APP_TOKEN"] = LocalBackendAuthentication.appToken
        environment["DJONEHUB_SUPPORT_DIR"] = FlameRuntimePaths.supportDirectory.path
        proc.environment = environment

        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe
        proc.terminationHandler = { [weak self] p in
            Task { @MainActor in
                guard let self else { return }
                self.process = nil
                self.childPID = 0
                if self.state == .running || self.state == .starting {
                    self.state = .stopped
                }
            }
        }

        stdoutTask = Task.detached(priority: .utility) { [weak self] in
            self?.drain(outPipe.fileHandleForReading, to: .standardOutput)
        }
        stderrTask = Task.detached(priority: .utility) { [weak self] in
            self?.drain(errPipe.fileHandleForReading, to: .standardError)
        }

        do {
            try proc.run()
        } catch {
            state = .failed("无法启动后端：\(error.localizedDescription)")
            return
        }

        process = proc
        childPID = proc.processIdentifier
        state = .starting

        Task { @MainActor [weak self] in
            guard let self else { return }
            // 等待 socket 就绪
            for _ in 0..<50 {
                if FileManager.default.fileExists(atPath: self.socketPath) {
                    self.state = .running
                    self.startAgentAudioTransport()
                    return
                }
                if !proc.isRunning {
                    self.state = .failed("后端进程提前退出，请查看日志")
                    return
                }
                try? await Task.sleep(nanoseconds: 100_000_000)
            }
            self.state = .failed("后端启动超时")
        }
    }

    func stop() {
        AgentAudioBridge.shared.stop()
        if let proc = process, proc.isRunning {
            proc.terminate()
        }
        stdoutTask?.cancel()
        stderrTask?.cancel()
    }

    private func startAgentAudioTransport() {
        let bridge = AgentAudioBridge.shared
        bridge.onStateChange = { state in
            Task { await Self.reportAgentAudioState(state, socketPath: bridge.socketPath) }
        }
        do {
            try bridge.start()
        } catch {
            Task {
                await Self.reportAgentAudioState(
                    AgentAudioBridgeState(transportReady: false, clientConnected: false,
                                          sessionNonce: nil, lastError: error.localizedDescription),
                    socketPath: bridge.socketPath)
            }
        }
    }

    private struct AgentAudioTransportUpdate: Encodable {
        let compiled: Bool
        let transportReady: Bool
        let moduleIOReady: Bool
        let socketPath: String
        let clientConnected: Bool
        let asrCompiled: Bool
        let asrRunning: Bool
        let asrError: String
        let error: String

        enum CodingKeys: String, CodingKey {
            case compiled
            case transportReady = "transport_ready"
            case moduleIOReady = "module_io_ready"
            case socketPath = "socket_path"
            case clientConnected = "client_connected"
            case asrCompiled = "asr_compiled"
            case asrRunning = "asr_running"
            case asrError = "asr_error"
            case error
        }
    }

    private struct AgentAudioTransportResponse: Decodable {}

    private static func reportAgentAudioState(_ state: AgentAudioBridgeState, socketPath: String) async {
        let update = AgentAudioTransportUpdate(
            compiled: true,
            transportReady: state.transportReady,
            moduleIOReady: state.moduleIOReady,
            socketPath: socketPath,
            clientConnected: state.clientConnected,
            asrCompiled: state.asrCompiled,
            asrRunning: state.asrRunning,
            asrError: state.asrError ?? "",
            error: state.lastError ?? "")

        // 后端 socket 文件出现后，HTTP 服务可能还没有完成监听。
        // 只尝试一次会把启动竞态永久固化为 compiled=false；状态注册必须可重试。
        for attempt in 0..<10 {
            do {
                let _: AgentAudioTransportResponse? = try await APIClient(timeoutInterval: 5).send(
                    "api/agent/audio/transport", method: "PUT", body: update)
                return
            } catch {
                guard attempt < 9 else { return }
                let delay = UInt64(100_000_000) * UInt64(attempt + 1)
                try? await Task.sleep(nanoseconds: delay)
            }
        }
    }

    private func drain(_ handle: FileHandle, to dest: FileHandle) {
        while true {
            let data = handle.availableData
            if data.isEmpty { break }
            try? dest.write(contentsOf: data)
        }
        try? handle.close()
    }
}
#endif

import Foundation
import Combine

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
        let support = fm.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
            .appendingPathComponent("DJOneHubNative", isDirectory: true)
        try? fm.createDirectory(at: support, withIntermediateDirectories: true)
        socketPath = support.appendingPathComponent("djonehub.sock").path
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
        if let proc = process, proc.isRunning {
            proc.terminate()
        }
        stdoutTask?.cancel()
        stderrTask?.cancel()
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

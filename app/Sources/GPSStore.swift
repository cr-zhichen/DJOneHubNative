import Combine
import Foundation

/// 模块定位页面状态。页面可见时每 15 秒读取一次后端缓存，模块控制始终由后端独占。
@MainActor
final class GPSStore: ObservableObject {
    private static let defaultPollIntervalSeconds = 15

    @Published private(set) var status: GPSStatus?
    @Published private(set) var refreshInFlight = false
    @Published private(set) var actionInFlight = false
    @Published var errorMessage: String?

    private var pollTask: Task<Void, Never>?

    deinit {
        pollTask?.cancel()
    }

    var canChangeEnabled: Bool {
        guard let status else { return false }
        return !actionInFlight
            && !refreshInFlight
            && status.supported != false
            && status.state != .checking
            && status.state != .unavailable
    }

    func beginPolling() {
        guard pollTask == nil else { return }
        refresh()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                let interval = max(
                    self?.status?.pollIntervalSeconds ?? Self.defaultPollIntervalSeconds,
                    1)
                do {
                    try await Task.sleep(nanoseconds: UInt64(interval) * 1_000_000_000)
                } catch {
                    return
                }
                guard !Task.isCancelled else { return }
                self?.refresh()
            }
        }
    }

    func endPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    func refresh(preservingError: Bool = false) {
        guard !refreshInFlight, !actionInFlight else { return }
        refreshInFlight = true
        Task { [weak self] in
            guard let self else { return }
            defer { refreshInFlight = false }
            do {
                let value: GPSStatus = try await APIClient(timeoutInterval: 20)
                    .get("api/gps/status")
                status = value
                if !preservingError {
                    errorMessage = nil
                }
            } catch {
                if !preservingError {
                    errorMessage = "读取模块定位状态失败：\(error.localizedDescription)"
                }
            }
        }
    }

    func setEnabled(_ enabled: Bool) {
        guard canChangeEnabled, status?.enabled != enabled else { return }
        actionInFlight = true
        errorMessage = nil
        Task { [weak self] in
            guard let self else { return }
            do {
                let path = enabled ? "api/gps/start" : "api/gps/stop"
                let value: GPSStatus = try await APIClient(timeoutInterval: 30).send(path)
                status = value
            } catch {
                errorMessage = "\(enabled ? "启用" : "关闭")模块定位失败：\(error.localizedDescription)"
                actionInFlight = false
                refresh(preservingError: true)
                return
            }
            actionInFlight = false
        }
    }
}

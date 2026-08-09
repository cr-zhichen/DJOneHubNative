import Combine
import Foundation

struct ESIMProfileContext: Identifiable {
    let eid: String
    let aidHex: String
    let profile: ProfileItem

    var id: String { "\(aidHex):\(profile.iccid)" }
}

struct ESIMBanner: Identifiable {
    enum Tone {
        case info
        case success
        case warning
        case error
    }

    let id = UUID()
    let tone: Tone
    let message: String
    let detail: String?
}

@MainActor
final class ESIMStore: ObservableObject {
    enum LoadPhase: Equatable {
        case idle
        case loading
        case loaded
        case failed(String)
    }

    enum Action: Equatable {
        case switching(String)
        case deleting(String)
        case probingPhonebook
        case downloading
    }

    @Published private(set) var overview: ESIMOverview?
    @Published private(set) var health: ESIMHealth?
    @Published private(set) var healthError: String?
    @Published private(set) var notes: [String: ProfileNote] = [:]
    @Published private(set) var notesError: String?
    @Published private(set) var moduleNotes: ModuleNotesResponse?
    @Published private(set) var moduleNotesError: String?
    @Published private(set) var loadPhase: LoadPhase = .idle
    @Published private(set) var isRefreshing = false
    @Published private(set) var action: Action?
    @Published private(set) var operation: ESIMOperationSnapshot?
    @Published var banner: ESIMBanner?

    private var loadTask: Task<Void, Never>?
    private var operationPollTask: Task<Void, Never>?
    private var loadGeneration = 0
    private var lastHandledOperationID: String?

    deinit {
        loadTask?.cancel()
        operationPollTask?.cancel()
    }

    var capabilities: ESIMCapabilities? { overview?.capabilities }
    var isPhysicalSIM: Bool { overview?.cardType == "physical_sim" }
    var isInitialLoading: Bool { loadPhase == .loading && overview == nil }
    var isBusy: Bool { isRefreshing || action != nil || operation?.isActive == true }

    var profileGroups: [(group: EUICCProfiles, profiles: [ESIMProfileContext])] {
        (overview?.profiles ?? []).map { group in
            let eid = group.eid ?? ""
            let aid = group.aidHex ?? ""
            return (group, (group.profiles ?? []).map {
                ESIMProfileContext(eid: eid, aidHex: aid, profile: $0)
            })
        }
    }

    var profileCount: Int {
        profileGroups.reduce(0) { $0 + $1.profiles.count }
    }

    func load(forceRefresh: Bool = false) {
        loadTask?.cancel()
        loadGeneration &+= 1
        let generation = loadGeneration
        if forceRefresh {
            isRefreshing = true
        } else if overview == nil {
            loadPhase = .loading
        }

        loadTask = Task { [weak self] in
            guard let self else { return }
            defer {
                if generation == self.loadGeneration {
                    self.isRefreshing = false
                    self.loadTask = nil
                }
            }
            do {
                let path = forceRefresh ? "api/esim?refresh=true" : "api/esim"
                let fetched: ESIMOverview = try await APIClient(timeoutInterval: 45).get(path)
                guard !Task.isCancelled, generation == loadGeneration else { return }
                overview = fetched
                operation = fetched.operation
                loadPhase = .loaded
                if fetched.operation?.isActive == true {
                    trackOperation(fetched.operation!)
                }
                await loadSecondaryData(generation: generation)
            } catch {
                guard !Task.isCancelled, generation == loadGeneration else { return }
                if overview == nil {
                    loadPhase = .failed(error.localizedDescription)
                } else {
                    banner = ESIMBanner(
                        tone: .error,
                        message: forceRefresh ? "刷新卡片信息失败" : "更新卡片信息失败",
                        detail: error.localizedDescription)
                }
            }
        }
    }

    func refresh() {
        guard capabilities?.canRefresh == true, !isBusy else { return }
        banner = nil
        load(forceRefresh: true)
    }

    func switchProfile(_ context: ESIMProfileContext) {
        guard capabilities?.canSwitch == true, !isBusy, !context.profile.isEnabled else { return }
        action = .switching(context.id)
        banner = nil
        Task {
            do {
                let result: ESIMSwitchResult = try await APIClient(timeoutInterval: 130).send(
                    "api/esim/switch",
                    body: ESIMSwitchRequest(iccid: context.profile.iccid, aid: context.aidHex))
                action = nil
                if let operation = result.operation {
                    trackOperation(operation)
                } else {
                    banner = ESIMBanner(
                        tone: .warning,
                        message: "Profile 切换已提交",
                        detail: "模块正在重连，请稍后刷新确认")
                }
            } catch {
                action = nil
                banner = ESIMBanner(tone: .error, message: "切换 Profile 失败", detail: error.localizedDescription)
            }
        }
    }

    func deleteProfile(_ context: ESIMProfileContext) {
        guard capabilities?.canDelete == true, !isBusy, !context.profile.isEnabled else { return }
        action = .deleting(context.id)
        banner = nil
        Task {
            defer { action = nil }
            do {
                let result: ESIMProfileResult = try await APIClient(timeoutInterval: 90).send(
                    "api/esim/profile", method: "DELETE",
                    body: ESIMDeleteRequest(iccid: context.profile.iccid, aid: context.aidHex))
                if let warning = result.warning, !warning.isEmpty {
                    banner = ESIMBanner(tone: .warning, message: "Profile 已删除", detail: warning)
                } else {
                    banner = ESIMBanner(tone: .success, message: "Profile 已删除", detail: nil)
                }
                load(forceRefresh: true)
            } catch {
                banner = ESIMBanner(tone: .error, message: "删除 Profile 失败", detail: error.localizedDescription)
            }
        }
    }

    func probePhonebook() {
        guard capabilities?.canProbePhonebook == true, !isBusy else { return }
        action = .probingPhonebook
        banner = nil
        Task {
            defer { action = nil }
            do {
                let result: PhonebookProbeResult = try await APIClient().send("api/esim/phonebook/probe")
                let parts = [
                    result.storageSupported == true ? "模块资料库可用" : "模块资料库不可用",
                    result.readSupported == true ? "可读取" : "不可读取",
                    result.writeSupported == true ? "可写入" : "不可写入",
                ]
                banner = ESIMBanner(
                    tone: result.writeSupported == true ? .success : .warning,
                    message: "通讯录检测完成",
                    detail: parts.joined(separator: "，"))
            } catch {
                banner = ESIMBanner(tone: .error, message: "通讯录检测失败", detail: error.localizedDescription)
            }
        }
    }

    func startDownload(_ request: ESIMDownloadRequest) {
        guard capabilities?.canDownload == true, !isBusy else { return }
        action = .downloading
        banner = nil
        Task {
            do {
                let started: ESIMOperationSnapshot = try await APIClient().send(
                    "api/esim/download", body: request)
                trackOperation(started)
            } catch {
                action = nil
                banner = ESIMBanner(tone: .error, message: "无法开始下载 Profile", detail: error.localizedDescription)
            }
        }
    }

    func reloadAfterMetadataChange() {
        load()
    }

    func dismissBanner() {
        banner = nil
    }

    func isProfileActionRunning(_ context: ESIMProfileContext) -> Bool {
        switch action {
        case .switching(let id), .deleting(let id): return id == context.id
        default: return false
        }
    }

    private func loadSecondaryData(generation: Int) async {
        async let notesRequest: (value: NotesResponse?, error: String?) = fetchSecondary("api/esim/notes")
        async let moduleRequest: (value: ModuleNotesResponse?, error: String?) = fetchSecondary("api/esim/module-notes")
        async let healthRequest: (value: ESIMHealth?, error: String?) = fetchSecondary("api/esim/health", timeout: 10)
        let (notesResponse, moduleResponse, healthResponse) = await (notesRequest, moduleRequest, healthRequest)
        guard !Task.isCancelled, generation == loadGeneration else { return }
        if let response = notesResponse.value {
            notes = response.notes ?? [:]
        }
        notesError = notesResponse.error
        moduleNotes = moduleResponse.value
        moduleNotesError = moduleResponse.error
        health = healthResponse.value
        healthError = healthResponse.error
        if let healthOperation = healthResponse.value?.operation, healthOperation.isActive {
            trackOperation(healthOperation)
        }
    }

    private func fetchSecondary<T: Decodable>(_ path: String, timeout: TimeInterval = 15) async -> (value: T?, error: String?) {
        do {
            let value: T = try await APIClient(timeoutInterval: timeout).get(path)
            return (value, nil)
        } catch {
            return (nil, error.localizedDescription)
        }
    }

    private func trackOperation(_ snapshot: ESIMOperationSnapshot) {
        operation = snapshot
        if snapshot.kind == "download_profile" && snapshot.isActive {
            action = .downloading
        }
        operationPollTask?.cancel()
        if !snapshot.isActive {
            handleTerminalOperation(snapshot)
            return
        }

        operationPollTask = Task { [weak self] in
            guard let self else { return }
            var consecutiveFailures = 0
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                guard !Task.isCancelled else { return }
                do {
                    let response: ESIMOperationResponse = try await APIClient(timeoutInterval: 10).get("api/esim/operation")
                    consecutiveFailures = 0
                    guard let current = response.operation else {
                        operationPollTask = nil
                        operation = nil
                        action = nil
                        banner = ESIMBanner(
                            tone: .warning,
                            message: "无法继续跟踪 eSIM 操作",
                            detail: "后端操作状态已重置，请刷新卡片确认最终结果")
                        return
                    }
                    guard current.id == snapshot.id else {
                        trackOperation(current)
                        return
                    }
                    operation = current
                    if !current.isActive {
                        operationPollTask = nil
                        handleTerminalOperation(current)
                        return
                    }
                } catch {
                    // 模块重启时短暂断开属于切卡预期状态；连续失败后才向用户提示。
                    consecutiveFailures += 1
                    if consecutiveFailures == 8 {
                        banner = ESIMBanner(
                            tone: .warning,
                            message: "仍在等待 eSIM 操作状态",
                            detail: "模块可能正在重启，应用会继续自动重试")
                    }
                }
            }
        }
    }

    private func handleTerminalOperation(_ snapshot: ESIMOperationSnapshot) {
        action = nil
        guard lastHandledOperationID != snapshot.id else { return }
        lastHandledOperationID = snapshot.id

        if snapshot.isFailed {
            banner = ESIMBanner(
                tone: .error,
                message: operationTitle(snapshot.kind, success: false),
                detail: snapshot.error ?? "操作未完成")
            return
        }
        if snapshot.hasWarning {
            banner = ESIMBanner(
                tone: .warning,
                message: snapshot.message ?? operationTitle(snapshot.kind, success: true),
                detail: snapshot.result?.warning)
        } else {
            banner = ESIMBanner(
                tone: .success,
                message: snapshot.message ?? operationTitle(snapshot.kind, success: true),
                detail: nil)
        }

        let delay = max(0, snapshot.refreshAfterSeconds ?? 0)
        Task { [weak self] in
            if delay > 0 {
                try? await Task.sleep(nanoseconds: UInt64(delay) * 1_000_000_000)
            }
            guard !Task.isCancelled else { return }
            self?.load(forceRefresh: true)
        }
    }

    private func operationTitle(_ kind: String, success: Bool) -> String {
        switch kind {
        case "download_profile": return success ? "Profile 下载完成" : "Profile 下载失败"
        case "switch_profile": return success ? "Profile 切换完成" : "Profile 切换失败"
        default: return success ? "eSIM 操作完成" : "eSIM 操作失败"
        }
    }
}

import Foundation
import Combine

/// 提示气泡内容
struct ToastItem: Identifiable, Equatable {
    let id = UUID()
    let message: String
    let isSuccess: Bool
    var title: String? = nil
    var icon: String? = nil
}

private enum NetworkRecoveryTrigger: Equatable {
    case wake
    case manual

    var delayNanoseconds: UInt64 {
        switch self {
        case .wake: return 12_000_000_000
        case .manual: return 0
        }
    }

    var waitingMessage: String {
        switch self {
        case .wake: return "正在等待 4G 网卡恢复…"
        case .manual: return "正在检查 4G 网卡…"
        }
    }

    var successMessage: String {
        switch self {
        case .wake: return "4G 网卡已自动恢复"
        case .manual: return "4G 网卡已恢复"
        }
    }
}

/// 首页数据缓存：后台持续轮询后端，页面切换时立即展示最近一次的数据，
/// 避免每次进入首页都重新拉取导致"读取模块状态…"闪烁。
@MainActor
final class DashboardStore: ObservableObject {
    /// 供 AppDelegate（通知点击）等非视图位置访问
    static weak var shared: DashboardStore?
    @Published var health: HealthStatus?
    @Published var status: DeviceStatus?
    @Published var traffic: TrafficSnapshot?
    @Published var lastUpdated: Date?
    /// 最近一次轮询失败：界面保留旧数据并提示"已过期"，而不是回到加载态
    @Published var statusStale = false

    /// 防止轮询请求叠加：上次未完成时跳过本次
    private var refreshInFlight = false
    private var callModeRefreshInFlight = false

    @Published var usbnetEnabled = false
    @Published var usbnetLoaded = false
    @Published var busy = false
    @Published private(set) var networkRecovering = false
    @Published private(set) var networkRecoveryMessage: String?

    @Published var check4G: CheckResult?

    @Published var services: [NetworkService] = []
    @Published var servicesLoaded = false
    @Published var orderMessage: String?
    @Published var orderError: String?

    @Published var toast: ToastItem?

    /// 接管短信保存模式（持久化到本机并清理 SIM/模块原始短信）
    @Published var smsAdopt = UserDefaults.standard.bool(forKey: "smsAdopt")

    /// 语音功能（USB 音频）
    @Published var voiceError: String?
    @Published var callModeStatus: CallModeStatus?
    @Published var callModeActionInFlight = false
    @Published var callModeError: String?
    @Published var callModeBackups: [CallModeUSBBackupSummary] = []
    @Published var callModeBackupsLoading = false
    @Published var callModeBackupError: String?
    @Published var callModeBackupExportingID: String?
    @Published var callModeBackupImporting = false
    @Published var callModeBackupDeletingID: String?
    @Published var callModeRestoreInFlight = false
    @Published var callStatus = CallStatus(state: "idle", number: nil, incoming: false, active: false)
    @Published var callAnswerInFlight = false
    @Published var dialNumber = ""
    @Published var audioError: String?
    @Published var audioRunning = false
    private var observedCallModeRestoreAt: Date?
    /// 本次来电时间（用于详情展示）
    @Published var incomingAt: Date?
    /// 通话记录
    @Published var callHistory: [CallRecord] = []
    /// 是否显示通话记录弹窗
    @Published var showCallHistory = false {
        didSet {
            if showCallHistory {
                AttentionStore.shared.markCallsViewed()
            }
        }
    }

    private let backend: BackendProcess
    private var timer: Timer?
    private var cancellables = Set<AnyCancellable>()
    private var networkRecoveryTask: Task<Void, Never>?
    private var lastAutomaticNetworkRecoveryAt: Date?
    private var audioActivationTask: Task<Void, Never>?

    private static let moduleNetworkDisconnectedError = "4G 模块网卡未连接"
    private static let automaticNetworkRecoveryCooldown: TimeInterval = 10 * 60

    init(backend: BackendProcess) {
        self.backend = backend
        backend.$state
            .receive(on: RunLoop.main)
            .sink { [weak self] state in
                guard let self else { return }
                switch state {
                case .running:
                    self.startPolling()
                    self.loadUSBStatus()
                    self.loadServices()
                    self.syncSMSAdopt()
                    self.pollCallModeStatus()
                    self.loadCallHistory()
                default:
                    self.stopPolling()
                    self.reset()
                }
            }
            .store(in: &cancellables)
    }

    private func reset() {
        networkRecoveryTask?.cancel()
        networkRecoveryTask = nil
        networkRecovering = false
        networkRecoveryMessage = nil
        health = nil
        status = nil
        traffic = nil
        lastUpdated = nil
        statusStale = false
        usbnetEnabled = false
        usbnetLoaded = false
        toast = nil
        callModeStatus = nil
        callModeActionInFlight = false
        callModeError = nil
        callModeBackups = []
        callModeBackupsLoading = false
        callModeBackupError = nil
        callModeBackupExportingID = nil
        callModeBackupImporting = false
        callModeBackupDeletingID = nil
        callModeRestoreInFlight = false
        observedCallModeRestoreAt = nil
        callStatus = CallStatus(state: "idle", number: nil, incoming: false, active: false)
        callAnswerInFlight = false
        incomingAt = nil
        IncomingCallCard.shared.hide()
        stopAudio()
    }

    private func startPolling() {
        guard timer == nil else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in
                guard let self, !self.networkRecovering else { return }
                guard self.audioActivationTask == nil else { return }
                self.refresh()
                self.pollCallStatus()
                self.pollCallModeStatus()
            }
        }
        refresh()
        pollCallStatus()
        pollCallModeStatus()
    }

    private func stopPolling() {
        timer?.invalidate()
        timer = nil
    }

    // MARK: - 数据刷新

    func refresh() {
        guard case .running = backend.state else { return }
        guard !refreshInFlight else { return }
        refreshInFlight = true
        let client = APIClient(timeoutInterval: 10)
        Task {
            defer { refreshInFlight = false }
            do {
                let h: HealthStatus = try await client.get("api/health")
                let s: DeviceStatus = try await client.get("api/status")
                let t: TrafficSnapshot = try await client.get("api/network/traffic")
                health = h
                status = s
                traffic = t
                lastUpdated = Date()
                statusStale = false
            } catch {
                // 保留上次数据，只标记过期，避免页面卡在"读取模块状态…"
                statusStale = true
            }
        }
    }

    // MARK: - USB 网卡

    func loadUSBStatus() {
        guard case .running = backend.state else { return }
        Task {
            do {
                let diag: NetworkDiagnostic = try await APIClient().get("api/network")
                if let mode = Int(diag.usbnetMode ?? "") {
                    usbnetEnabled = (mode == 1)
                }
                usbnetLoaded = true
            } catch {
                usbnetLoaded = true
            }
        }
    }

    func switchMode() {
        guard busy == false, !networkRecovering else { return }
        busy = true
        let targetMode = usbnetEnabled ? 1 : 0
        Task {
            do {
                let client = APIClient()
                let _: USBNetResult = try await client.send(
                    "api/network/usbnet", body: USBNetRequest(mode: targetMode))
                busy = false
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                loadUSBStatus()
                // 模式切换后模块 USB 重新枚举，刷新服务列表以识别模块网卡
                loadServices()
            } catch {
                busy = false
                loadUSBStatus()
            }
        }
    }

    func reboot() {
        guard busy == false, !networkRecovering else { return }
        busy = true
        Task {
            do {
                let client = APIClient()
                let _: RebootResult = try await client.send("api/network/reboot-module")
                busy = false
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                loadUSBStatus()
            } catch {
                busy = false
            }
        }
    }

    var canRetryNetworkConnection: Bool {
        guard let traffic else { return false }
        return isRecoverableNetworkFailure(traffic) && usbnetEnabled
    }

    /// 系统唤醒后给 macOS 留出自然恢复时间；仍未恢复时只重启模块一次。
    func recoverNetworkAfterWake() {
        guard usbnetEnabled else { return }
        if let lastAutomaticNetworkRecoveryAt,
           Date().timeIntervalSince(lastAutomaticNetworkRecoveryAt)
               < Self.automaticNetworkRecoveryCooldown {
            return
        }
        scheduleNetworkRecovery(.wake)
    }

    /// 首页错误态的手动恢复入口，与唤醒后的自动恢复共用同一流程。
    func retryNetworkConnection() {
        scheduleNetworkRecovery(.manual)
    }

    private func scheduleNetworkRecovery(_ trigger: NetworkRecoveryTrigger) {
        guard case .running = backend.state else {
            if trigger == .manual {
                showToast(message: "后台服务尚未运行", isSuccess: false, title: "无法重新连接")
            }
            return
        }
        guard networkRecoveryTask == nil else { return }

        networkRecovering = true
        networkRecoveryMessage = trigger.waitingMessage
        networkRecoveryTask = Task { [weak self] in
            guard let self else { return }
            defer {
                self.networkRecoveryTask = nil
                self.networkRecovering = false
                self.networkRecoveryMessage = nil
            }

            if trigger.delayNanoseconds > 0 {
                do {
                    try await Task.sleep(nanoseconds: trigger.delayNanoseconds)
                } catch {
                    return
                }
            }
            await self.performNetworkRecovery(trigger)
        }
    }

    private func performNetworkRecovery(_ trigger: NetworkRecoveryTrigger) async {
        guard !busy, callModeStatus?.isBusy != true else {
            if trigger == .manual {
                showToast(message: "模块正在执行其他操作，请稍后重试", isSuccess: false, title: "无法重新连接")
            }
            return
        }

        let client = APIClient(timeoutInterval: 10)
        do {
            let current: TrafficSnapshot = try await client.get("api/network/traffic")
            traffic = current
            if current.available {
                if trigger == .manual {
                    showToast(message: "4G 网卡当前已连接", isSuccess: true, title: "无需重试")
                }
                return
            }
            guard isRecoverableNetworkFailure(current) else {
                if trigger == .manual {
                    showToast(
                        message: current.error ?? "当前故障无法通过模块重启恢复",
                        isSuccess: false,
                        title: "无法重新连接")
                }
                return
            }
            guard usbnetEnabled else {
                if trigger == .manual {
                    showToast(message: "请先开启 USB 网卡", isSuccess: false, title: "无法重新连接")
                }
                return
            }

            busy = true
            defer { busy = false }

            let latestDeviceStatus: DeviceStatus = try await client.get("api/status")
            status = latestDeviceStatus
            if let usbnetMode = latestDeviceStatus.usbnetMode {
                usbnetEnabled = (usbnetMode == 1)
            }
            guard latestDeviceStatus.usbnetMode == 1 else {
                if trigger == .manual {
                    showToast(message: "请先开启 USB 网卡", isSuccess: false, title: "无法重新连接")
                }
                return
            }

            let latestCallStatus: CallStatus = try await client.get("api/call/status")
            updateCallStatus(latestCallStatus)
            guard latestCallStatus.isIdle else {
                if trigger == .manual {
                    showToast(message: "通话期间不能重启模块", isSuccess: false, title: "无法重新连接")
                }
                return
            }

            networkRecoveryMessage = "正在重启 4G 模块…"
            if trigger == .wake {
                lastAutomaticNetworkRecoveryAt = Date()
            }
            let _: RebootResult = try await client.send("api/network/reboot-module")
            networkRecoveryMessage = "正在等待 4G 网卡重新连接…"

            var latestTraffic = current
            for _ in 0..<30 {
                try await Task.sleep(nanoseconds: 2_000_000_000)
                try Task.checkCancellation()
                do {
                    let sample: TrafficSnapshot = try await client.get("api/network/traffic")
                    latestTraffic = sample
                    traffic = sample
                    if sample.available {
                        loadUSBStatus()
                        loadServices()
                        refresh()
                        showToast(message: trigger.successMessage, isSuccess: true, title: "4G 网络")
                        return
                    }
                } catch {
                    // 模块重启期间 socket 可用但硬件会短暂消失，继续等待重新枚举。
                }
            }

            traffic = latestTraffic
            showToast(
                message: "等待 60 秒后仍未恢复，请重试或拔插模块",
                isSuccess: false,
                title: "4G 网卡恢复失败")
        } catch {
            guard !Task.isCancelled else { return }
            showToast(
                message: "重新连接失败：\(error.localizedDescription)",
                isSuccess: false,
                title: "4G 网卡恢复失败")
        }
    }

    private func isRecoverableNetworkFailure(_ snapshot: TrafficSnapshot) -> Bool {
        !snapshot.available
            && snapshot.interface?.isEmpty == false
            && snapshot.error == Self.moduleNetworkDisconnectedError
    }

    // MARK: - 连通性检查

    func runCheck4G() {
        guard busy == false, !networkRecovering else { return }
        busy = true
        Task {
            do {
                let client = APIClient()
                let result: CheckResult = try await client.send("api/network/check-4g")
                busy = false
                showToast(
                    message: [result.summary, result.detail].compactMap { $0 }.joined(separator: "\n"),
                    isSuccess: result.ok)
            } catch {
                busy = false
                showToast(message: "检查失败：\(error.localizedDescription)", isSuccess: false)
            }
        }
    }

    // MARK: - 短信接管

    /// 启动后从后端拉取接管模式真实状态，保证 UI 与后端一致
    func syncSMSAdopt() {
        Task {
            struct AdoptResponse: Decodable { let enabled: Bool }
            do {
                let r: AdoptResponse = try await APIClient().get("api/sms/adopt")
                UserDefaults.standard.set(r.enabled, forKey: "smsAdopt")
                smsAdopt = r.enabled
            } catch {
                // 后端不可用：用本地值同步到后端
                try? await APIClient().send("api/sms/adopt", body: SMSAdoptRequest(enabled: smsAdopt))
            }
        }
    }

    func setSMSAdopt(_ enabled: Bool) {
        guard smsAdopt != enabled else { return }
        UserDefaults.standard.set(enabled, forKey: "smsAdopt")
        smsAdopt = enabled
        Task {
            try? await APIClient().send("api/sms/adopt", body: SMSAdoptRequest(enabled: enabled))
        }
    }

    // MARK: - 语音与通话

    func pollCallModeStatus() {
        guard case .running = backend.state else { return }
        guard audioActivationTask == nil else { return }
        guard !callModeRefreshInFlight else { return }
        callModeRefreshInFlight = true
        Task {
            defer { callModeRefreshInFlight = false }
            do {
                let previousBackupPath = callModeStatus?.backupPath
                let status: CallModeStatus = try await APIClient(timeoutInterval: 12)
                    .get("api/call-mode/status")
                callModeStatus = status
                if status.isBusy || status.isReady {
                    callModeError = nil
                }
                if let backupPath = status.backupPath,
                   !backupPath.isEmpty,
                   backupPath != previousBackupPath {
                    loadCallModeBackups()
                }
                if let restore = status.lastRestore {
                    let isNewRestore = observedCallModeRestoreAt != restore.restoredAt
                    observedCallModeRestoreAt = restore.restoredAt
                    if isNewRestore && callModeRestoreInFlight {
                        callModeRestoreInFlight = false
                        loadCallModeBackups()
                        showToast(
                            message: restore.changed
                                ? "模块原始 USB 配置已还原并完成重启验证。"
                                : "当前模块配置已经与所选备份一致。",
                            isSuccess: true,
                            title: "模块配置")
                    }
                }
                if status.isReady && callStatus.isActive && !audioRunning {
                    startAudio()
                }
                if !status.isBusy {
                    callModeActionInFlight = false
                    if status.state == "failed" {
                        callModeRestoreInFlight = false
                    }
                }
            } catch {
                callModeError = "读取通话模式失败：\(error.localizedDescription)"
            }
        }
    }

    func enableCallMode(confirmADBAuthorization: Bool) {
        guard callModeActionInFlight == false else { return }
        callModeActionInFlight = true
        callModeError = nil
        Task {
            do {
                let status: CallModeStatus = try await APIClient(timeoutInterval: 15).send(
                    "api/call-mode/enable",
                    body: CallModeEnableRequest(
                        confirm: true,
                        confirmADBAuthorization: confirmADBAuthorization))
                callModeStatus = status
            } catch {
                callModeActionInFlight = false
                callModeError = "开启通话模式失败：\(error.localizedDescription)"
            }
        }
    }

    func downloadCallModeRuntime(source: String) {
        guard callModeActionInFlight == false else { return }
        callModeActionInFlight = true
        callModeError = nil
        Task {
            do {
                let status: CallModeStatus = try await APIClient(timeoutInterval: 15).send(
                    "api/call-mode/download",
                    body: CallModeDownloadRequest(confirm: true, source: source))
                callModeStatus = status
            } catch {
                callModeActionInFlight = false
                callModeError = "下载运行时失败：\(error.localizedDescription)"
            }
        }
    }

    func retryCallModePreparation() {
        guard callModeActionInFlight == false else { return }
        callModeActionInFlight = true
        callModeError = nil
        Task {
            do {
                let status: CallModeStatus = try await APIClient(timeoutInterval: 15)
                    .send("api/call-mode/retry")
                callModeStatus = status
                if !status.isBusy {
                    callModeActionInFlight = false
                }
            } catch {
                callModeActionInFlight = false
                callModeError = "重新准备通话模式失败：\(error.localizedDescription)"
            }
        }
    }

    func loadCallModeBackups() {
        guard case .running = backend.state else { return }
        guard !callModeBackupsLoading else { return }
        callModeBackupsLoading = true
        callModeBackupError = nil
        Task {
            defer { callModeBackupsLoading = false }
            do {
                let response: CallModeUSBBackupListResponse = try await APIClient(timeoutInterval: 12)
                    .get("api/call-mode/backups")
                callModeBackups = response.backups
            } catch {
                callModeBackupError = "读取配置备份失败：\(error.localizedDescription)"
            }
        }
    }

    func exportCallModeBackup(_ backup: CallModeUSBBackupSummary, to destination: URL) {
        guard !callModeBackupFileOperationInFlight else { return }
        guard let encodedID = backup.id.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) else {
            callModeBackupError = "备份文件名无法安全编码。"
            return
        }
        callModeBackupExportingID = backup.id
        callModeBackupError = nil
        Task {
            defer { callModeBackupExportingID = nil }
            do {
                let data = try await APIClient(timeoutInterval: 20)
                    .getData("api/call-mode/backups/export?id=\(encodedID)")
                try data.write(to: destination, options: .atomic)
                showToast(
                    message: "备份已保存到 \(destination.path)",
                    isSuccess: true,
                    title: "配置备份")
            } catch {
                callModeBackupError = "导出配置备份失败：\(error.localizedDescription)"
            }
        }
    }

    func importCallModeBackup(from source: URL) {
        guard !callModeBackupFileOperationInFlight else { return }
        callModeBackupImporting = true
        callModeBackupError = nil
        Task {
            defer { callModeBackupImporting = false }
            let accessGranted = source.startAccessingSecurityScopedResource()
            defer {
                if accessGranted {
                    source.stopAccessingSecurityScopedResource()
                }
            }
            do {
                let values = try source.resourceValues(forKeys: [.isRegularFileKey, .fileSizeKey])
                guard values.isRegularFile == true,
                      let fileSize = values.fileSize,
                      fileSize > 0,
                      fileSize <= 64 * 1_024 else {
                    callModeBackupError = "导入配置备份失败：请选择不超过 64 KB 的 JSON 文件。"
                    return
                }
                let data = try Data(contentsOf: source, options: [.mappedIfSafe])
                let response: CallModeUSBBackupImportResponse = try await APIClient(timeoutInterval: 20)
                    .upload("api/call-mode/backups/import", data: data)
                loadCallModeBackups()
                showToast(
                    message: "已导入 \(response.backup.savedAt.formatted(date: .numeric, time: .shortened)) 的模块配置备份。",
                    isSuccess: true,
                    title: "配置备份")
            } catch {
                callModeBackupError = "导入配置备份失败：\(error.localizedDescription)"
            }
        }
    }

    func deleteCallModeBackup(_ backup: CallModeUSBBackupSummary) {
        guard !callModeBackupFileOperationInFlight, !callModeActionInFlight else { return }
        callModeBackupDeletingID = backup.id
        callModeBackupError = nil
        Task {
            defer { callModeBackupDeletingID = nil }
            do {
                let response: CallModeUSBBackupDeleteResponse = try await APIClient(timeoutInterval: 20).send(
                    "api/call-mode/backups/delete",
                    body: CallModeBackupDeleteRequest(confirm: true, backupID: backup.id))
                guard response.deleted else {
                    callModeBackupError = "删除配置备份失败：服务没有确认删除。"
                    return
                }
                callModeBackups.removeAll { $0.id == response.backupID }
                showToast(message: "配置备份已删除。", isSuccess: true, title: "配置备份")
            } catch {
                callModeBackupError = "删除配置备份失败：\(error.localizedDescription)"
            }
        }
    }

    func restoreCallModeBackup(_ backup: CallModeUSBBackupSummary) {
        guard callModeActionInFlight == false else { return }
        callModeActionInFlight = true
        callModeRestoreInFlight = true
        callModeError = nil
        Task {
            do {
                let status: CallModeStatus = try await APIClient(timeoutInterval: 15).send(
                    "api/call-mode/restore",
                    body: CallModeRestoreRequest(confirm: true, backupID: backup.id))
                callModeStatus = status
            } catch {
                callModeActionInFlight = false
                callModeRestoreInFlight = false
                callModeError = "还原模块配置失败：\(error.localizedDescription)"
            }
        }
    }

    /// 轮询通话状态（并入 2 秒刷新）
    func pollCallStatus() {
        guard case .running = backend.state else { return }
        Task {
            do {
                let status: CallStatus = try await APIClient().get("api/call/status")
                updateCallStatus(status)
            } catch {
                // 查询失败时标记未知状态，避免 UI 卡在旧状态
                updateCallStatus(CallStatus(state: "unknown", number: callStatus.number, incoming: false, active: false))
            }
        }
    }

    private func updateCallStatus(_ status: CallStatus) {
        let previous = callStatus
        callStatus = status
        // 新来电（从非来电变为来电）：弹出自定义通知卡片并响铃
        if status.isIncoming && !previous.isIncoming {
            incomingAt = Date()
            IncomingCallCard.shared.show(store: self)
        }
        if previous.isIncoming && !status.isIncoming {
            IncomingCallCard.shared.hide()
        }
        // 只有蜂窝通话真正进入 active 后才建立模块 D4/UAC 路由，避免拨号音、
        // 未接通呼叫或来电铃声提前占用模块音频设备。
        if status.isActive && !previous.isActive {
            startAudio()
        }
        // 通话结束（非空闲 → 空闲）时清空来电信息
        if status.isIdle && (previous.isActive || previous.state == "unknown" || previous.isIncoming) {
            stopAudio()
            incomingAt = nil
            IncomingCallCard.shared.hide()
            loadCallHistory()
        }
    }

    // MARK: - 通话记录

    /// 拉取通话记录（后端按最新在前返回）
    func loadCallHistory() {
        guard case .running = backend.state else { return }
        Task {
            do {
                let list: [CallRecord] = try await APIClient().get("api/calls")
                AttentionStore.shared.reconcileCallHistory(list)
                callHistory = list
            } catch {
                // 拉取失败保持旧数据
            }
        }
    }

    /// 清空全部通话记录
    func clearCallHistory() {
        Task {
            do {
                let _: CallClearResult = try await APIClient().send("api/calls/clear")
                callHistory = []
                AttentionStore.shared.markCallsViewed()
            } catch {
                toast = ToastItem(message: "清空失败：\(error.localizedDescription)", isSuccess: false, title: "通话记录")
            }
        }
    }

    /// 删除单条通话记录
    func deleteCallRecord(_ id: String) {
        Task {
            do {
                let _: CallClearResult = try await APIClient().send("api/calls/delete", body: CallDeleteRequest(id: id))
                callHistory.removeAll { $0.id == id }
            } catch {
                toast = ToastItem(message: "删除失败：\(error.localizedDescription)", isSuccess: false, title: "通话记录")
            }
        }
    }

    func dial(_ number: String) {
        let trimmed = number.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        voiceError = nil
        Task {
            do {
                let _: CallActionResult = try await APIClient().send(
                    "api/call/dial", body: CallDialRequest(number: trimmed))
                pollCallStatus()
            } catch {
                voiceError = "拨号失败：\(error.localizedDescription)"
            }
        }
    }

    func answerCall() {
        guard !callAnswerInFlight else { return }
        guard callStatus.isIncoming else {
            voiceError = "当前没有可接听的来电。"
            return
        }
        guard callModeStatus?.isReady == true else {
            voiceError = "通话模式尚未就绪，暂时不能接听。"
            return
        }
        voiceError = nil
        callAnswerInFlight = true
        Task {
            defer { callAnswerInFlight = false }
            do {
                let _: CallActionResult = try await APIClient().send("api/call/answer")
                pollCallStatus()
            } catch {
                voiceError = "接听失败：\(error.localizedDescription)"
            }
        }
    }

    private var callModeBackupFileOperationInFlight: Bool {
        callModeBackupExportingID != nil
            || callModeBackupImporting
            || callModeBackupDeletingID != nil
    }

    func hangup() {
        voiceError = nil
        // 先停止主机 IO，模块侧路由由 /hangup 在 ATH 成功后清理，确保
        // voiceUSBTransition 不会让清理操作抢在挂断命令前面。
        stopAudio(stopModuleRoute: false)
        Task {
            do {
                let result: CallActionResult = try await APIClient().send("api/call/hangup")
                if let warning = result.warning, !warning.isEmpty {
                    audioError = warning
                }
                pollCallStatus()
            } catch {
                // ATH 失败（可能通话已由对方结束）：刷新状态同步 UI
                voiceError = "挂断失败：\(error.localizedDescription)"
                pollCallStatus()
            }
        }
    }

    private func startAudio() {
        guard audioActivationTask == nil, !audioRunning else { return }
        guard callModeStatus?.isReady == true else {
            audioError = "通话已接通，但通话模式尚未就绪。请挂断后完成运行时准备。"
            return
        }
        audioError = nil
        audioActivationTask = Task { [weak self] in
            guard let self else { return }
            defer { self.audioActivationTask = nil }
            do {
                guard await AudioBridge.shared.requestMicrophoneAccess() else {
                    self.audioError = "麦克风权限未开启。请在系统设置“隐私与安全性 → 麦克风”中允许 DJOneHub。"
                    return
                }
                let result: CallAudioStartResult = try await APIClient(timeoutInterval: 60)
                    .send("api/call/audio/start")
                guard result.started else {
                    self.audioError = "模块没有确认通话音频路由已启动。"
                    return
                }
                try Task.checkCancellation()
                guard self.callStatus.isActive else {
                    let _: CallAudioStopResult? = try? await APIClient(timeoutInterval: 20)
                        .send("api/call/audio/stop")
                    return
                }
                if let error = AudioBridge.shared.start() {
                    self.audioError = error
                    self.audioRunning = false
                    let _: CallAudioStopResult? = try? await APIClient(timeoutInterval: 20)
                        .send("api/call/audio/stop")
                    return
                }
                self.audioRunning = true
            } catch is CancellationError {
                // stopAudio 决定由普通停止接口还是 /hangup 负责模块侧清理，
                // 避免两个清理请求与 ATH 竞争同一个 USB 过渡锁。
            } catch {
                self.audioError = "通话音频启动失败：\(error.localizedDescription)"
                self.audioRunning = false
            }
        }
    }

    func stopAudio(stopModuleRoute: Bool = true) {
        let shouldStopModuleRoute = stopModuleRoute && (audioRunning || audioActivationTask != nil)
        audioActivationTask?.cancel()
        audioActivationTask = nil
        AudioBridge.shared.stop()
        audioRunning = false
        if shouldStopModuleRoute, case .running = backend.state {
            Task {
                do {
                    let _: CallAudioStopResult = try await APIClient(timeoutInterval: 20)
                        .send("api/call/audio/stop")
                } catch {
                    audioError = "通话音频清理失败：\(error.localizedDescription)"
                }
            }
        }
    }

    // MARK: - 提示气泡

    private var toastDismissTask: Task<Void, Never>?

    private func showToast(message: String, isSuccess: Bool, title: String? = nil) {
        toast = ToastItem(message: message, isSuccess: isSuccess, title: title)
        scheduleToastDismiss()
    }

    /// 鼠标悬停时延长显示时间
    func extendToast() {
        scheduleToastDismiss()
    }

    private func scheduleToastDismiss() {
        toastDismissTask?.cancel()
        toastDismissTask = Task {
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            if !Task.isCancelled {
                toast = nil
            }
        }
    }

    // MARK: - 网卡优先级

    func loadServices() {
        guard case .running = backend.state else { return }
        Task {
            do {
                struct ServicesResponse: Decodable { let services: [NetworkService] }
                let result: ServicesResponse = try await APIClient().get("api/network/services")
                services = result.services
                servicesLoaded = true
                orderError = nil
            } catch {
                servicesLoaded = true
            }
        }
    }

    func moveServiceUp(_ index: Int) {
        guard index > 0, index < services.count else { return }
        services.swapAt(index - 1, index)
    }

    func moveServices(from source: IndexSet, to destination: Int) {
        services.move(fromOffsets: source, toOffset: destination)
    }

    func moveService(_ from: Int, to: Int) {
        guard from != to, from >= 0, to >= 0, from < services.count, to < services.count else { return }
        let item = services.remove(at: from)
        services.insert(item, at: to)
    }

    func saveOrder() {
        guard !services.isEmpty else { return }
        orderError = nil
        orderMessage = nil
        Task {
            do {
                let client = APIClient()
                let result: MessageResponse = try await client.send(
                    "api/network/services-order", method: "PUT",
                    body: ServicesOrderRequest(services: services.map { $0.name }))
                orderMessage = result.message
                loadServices()
            } catch {
                orderError = error.localizedDescription
            }
        }
    }
}

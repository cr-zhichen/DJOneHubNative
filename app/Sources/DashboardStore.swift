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
    @Published var voiceEnabled = UserDefaults.standard.bool(forKey: "voiceEnabled")
    @Published var voiceSwitching = false
    @Published var voiceError: String?
    @Published var callStatus = CallStatus(state: "idle", number: nil, incoming: false, active: false)
    @Published var dialNumber = ""
    @Published var audioError: String?
    @Published var audioRunning = false
    /// 本次来电时间（用于详情展示）
    @Published var incomingAt: Date?
    /// 是否显示来电详情弹窗
    @Published var showCallDetail = false
    /// 通话记录
    @Published var callHistory: [CallRecord] = []
    /// 是否显示通话记录弹窗
    @Published var showCallHistory = false

    private let backend: BackendProcess
    private var timer: Timer?
    private var cancellables = Set<AnyCancellable>()
    private var networkRecoveryTask: Task<Void, Never>?
    private var lastAutomaticNetworkRecoveryAt: Date?

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
                    self.syncVoiceEnabled()
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
        callStatus = CallStatus(state: "idle", number: nil, incoming: false, active: false)
        stopAudio()
    }

    private func startPolling() {
        guard timer == nil else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in
                guard let self, !self.networkRecovering else { return }
                self.refresh()
                self.pollCallStatus()
            }
        }
        refresh()
        pollCallStatus()
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
        guard !busy, !voiceSwitching else {
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

    func syncVoiceEnabled() {
        Task {
            do {
                let r: VoiceEnabledResponse = try await APIClient().get("api/voice/enabled")
                UserDefaults.standard.set(r.enabled, forKey: "voiceEnabled")
                voiceEnabled = r.enabled
            } catch {
                try? await APIClient().send("api/voice/enable", body: VoiceEnableRequest(enabled: voiceEnabled))
            }
        }
    }

    func setVoiceEnabled(_ enabled: Bool) {
        guard voiceEnabled != enabled else { return }
        voiceEnabled = enabled
        voiceSwitching = true
        voiceError = nil
        Task {
            do {
                let result: VoiceEnableResult = try await APIClient().send(
                    "api/voice/enable", body: VoiceEnableRequest(enabled: enabled))
                voiceSwitching = false
                UserDefaults.standard.set(enabled, forKey: "voiceEnabled")
                // 模块会自行重启生效，无需用户手动重新插拔
                voiceError = nil
            } catch {
                voiceSwitching = false
                voiceError = "切换失败：\(error.localizedDescription)"
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
        Task {
            do {
                let _: CallActionResult = try await APIClient().send(
                    "api/call/dial", body: CallDialRequest(number: trimmed))
                startAudio()
                await pollCallStatus()
            } catch {
                voiceError = "拨号失败：\(error.localizedDescription)"
            }
        }
    }

    func answerCall() {
        Task {
            do {
                let _: CallActionResult = try await APIClient().send("api/call/answer")
                startAudio()
            } catch {
                voiceError = "接听失败：\(error.localizedDescription)"
            }
        }
    }

    func hangup() {
        Task {
            do {
                let _: CallActionResult = try await APIClient().send("api/call/hangup")
                stopAudio()
                await pollCallStatus()
            } catch {
                // ATH 失败（可能通话已由对方结束）：刷新状态同步 UI
                voiceError = "挂断失败：\(error.localizedDescription)"
                await pollCallStatus()
            }
        }
    }

    func startAudio() {
        let error = AudioBridge.shared.start()
        audioError = error
        audioRunning = error == nil
    }

    func stopAudio() {
        AudioBridge.shared.stop()
        audioError = nil
        audioRunning = false
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

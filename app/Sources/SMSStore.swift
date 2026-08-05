import Foundation
import Combine
import AppKit
import UserNotifications

/// 短信数据缓存：后台持续轮询后端，检测到新收到的短信时弹出 macOS 系统通知。
@MainActor
final class SMSStore: ObservableObject {
    static let shared = SMSStore(backend: BackendProcess.shared)

    @Published var items: [SMSItem] = []
    @Published var status: SMSStatus?
    @Published var storage: SMSStorageResponse?
    @Published var lastError: String?
    @Published var busy = false
    /// 当前是否正在查看短信页（正在查看时不再重复弹通知）
    @Published var viewingSMS = false
    /// 点击通知后要打开的会话号码（由短信页消费后清空）
    @Published var pendingOpenSender: String?

    /// 新短信通知开关（默认开启，持久化到 UserDefaults）
    @Published var notificationsEnabled: Bool {
        didSet {
            UserDefaults.standard.set(notificationsEnabled, forKey: Self.enabledKey)
        }
    }

    private static let enabledKey = "smsNotificationsEnabled"
    private static let seenKey = "smsSeenIDs"

    private let backend: BackendProcess
    private var timer: Timer?
    private var refreshInFlight = false
    private var cancellables = Set<AnyCancellable>()
    /// 首次拉取仅记录已存在的短信，不弹通知，避免启动时把历史短信全部通知一遍
    private var didSeed = false
    private var seenIDs = Set<String>()
    private var seenOrder: [String] = []
    /// 防止并发重复弹出授权框
    private var authRequestInFlight = false

    init(backend: BackendProcess) {
        self.backend = backend
        if UserDefaults.standard.object(forKey: Self.enabledKey) == nil {
            UserDefaults.standard.set(true, forKey: Self.enabledKey)
        }
        notificationsEnabled = UserDefaults.standard.bool(forKey: Self.enabledKey)
        seenOrder = UserDefaults.standard.stringArray(forKey: Self.seenKey) ?? []
        seenIDs = Set(seenOrder)

        backend.$state
            .receive(on: RunLoop.main)
            .sink { [weak self] state in
                guard let self else { return }
                switch state {
                case .running:
                    self.startPolling()
                default:
                    self.stopPolling()
                }
            }
            .store(in: &cancellables)
    }

    // MARK: - 轮询

    private func startPolling() {
        guard timer == nil else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.refresh()
            }
        }
        refresh()
    }

    private func stopPolling() {
        timer?.invalidate()
        timer = nil
    }

    func refresh(force: Bool = false) {
        guard case .running = backend.state else { return }
        guard !refreshInFlight else { return }
        refreshInFlight = true
        if force { busy = true }
        let client = APIClient()
        Task {
            defer { refreshInFlight = false }
            do {
                let st: SMSStatus = try await client.get("api/sms/status")
                if force || items.isEmpty {
                    let _: SMSRefreshResult = try await client.send("api/sms/refresh")
                }
                let list: [SMSItem] = try await client.get("api/sms")
                let storageRes: SMSStorageResponse? = try? await client.get("api/sms/storage")
                status = st
                storage = storageRes
                lastError = nil
                busy = false
                await handleNewMessages(list)
                items = list
            } catch {
                busy = false
                lastError = error.localizedDescription
            }
        }
    }

    // MARK: - 新短信检测与通知

    /// 确保已获得通知授权（未决定时弹出系统授权框），返回是否可用
    func ensureAuthorization() async -> Bool {
        let center = UNUserNotificationCenter.current()
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .authorized, .provisional, .ephemeral:
            return true
        case .notDetermined:
            guard !authRequestInFlight else { return false }
            authRequestInFlight = true
            defer { authRequestInFlight = false }
            do {
                return try await center.requestAuthorization(options: [.alert, .sound])
            } catch {
                return false
            }
        case .denied:
            return false
        @unknown default:
            return false
        }
    }

    private func handleNewMessages(_ list: [SMSItem]) async {
        let incoming = list.filter { !$0.isOutgoing }
        let currentIDs = Set(incoming.map(\.id))
        let shouldNotify = notificationsEnabled && didSeed
        let newOnes = shouldNotify ? incoming.filter { !seenIDs.contains($0.id) } : []
        markSeen(currentIDs)
        if !didSeed {
            didSeed = true
            return
        }
        guard !newOnes.isEmpty else { return }
        // 正在短信页查看时无需再弹通知（App 在前台）
        if viewingSMS && NSApp.isActive { return }
        // 未授权时请求授权（首次收到短信时弹出系统授权框）
        guard await ensureAuthorization() else { return }
        for item in newOnes.sorted(by: { $0.timestamp < $1.timestamp }) {
            postNotification(for: item)
        }
    }

    private func markSeen(_ ids: Set<String>) {
        for id in ids where !seenIDs.contains(id) {
            seenIDs.insert(id)
            seenOrder.append(id)
        }
        // 只保留最近 2000 条记录，避免无限增长
        if seenOrder.count > 2000 {
            let overflow = seenOrder.count - 2000
            for id in seenOrder.prefix(overflow) {
                seenIDs.remove(id)
            }
            seenOrder.removeFirst(overflow)
        }
        UserDefaults.standard.set(Array(seenOrder), forKey: Self.seenKey)
    }

    private func postNotification(for item: SMSItem) {
        let content = UNMutableNotificationContent()
        content.title = item.sender ?? "新短信"
        content.body = String((item.content ?? "").prefix(300))
        content.sound = .default
        content.userInfo = ["sender": item.sender ?? ""]
        let request = UNNotificationRequest(
            identifier: "sms-" + UUID().uuidString,
            content: content,
            trigger: nil)
        UNUserNotificationCenter.current().add(request)
    }

    // MARK: - 通知点击

    /// 点击通知后打开短信页（由 AppDelegate 调用）
    func requestOpenSMS(sender: String?) {
        pendingOpenSender = (sender?.isEmpty ?? true) ? nil : sender
    }

    /// 通知已消费：短信页处理完打开请求后调用
    func consumeOpenRequest() {
        pendingOpenSender = nil
    }

    // MARK: - 调试：模拟通知

    /// 模拟一条新短信通知（走与真实短信相同的发送路径），返回结果描述
    func simulateNotification() async -> String {
        guard await ensureAuthorization() else {
            let settings = await UNUserNotificationCenter.current().notificationSettings()
            if settings.authorizationStatus == .denied {
                return "通知权限已被拒绝，请在系统设置中开启"
            }
            return "通知权限未授予"
        }
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        // 已授权但横幅被关闭（静默）时提醒用户
        if settings.alertSetting == .disabled {
            return "通知已发送到通知中心，但横幅/提示被系统关闭，请点击\"打开通知设置\"将样式改为横幅"
        }
        let item = SMSItem(
            sender: "+8613800000000",
            content: "这是一条模拟短信，用于测试通知功能。",
            code: nil,
            timestamp: Date(),
            moduleStorage: nil,
            moduleIndex: nil,
            archived: nil,
            direction: nil)
        postNotification(for: item)
        return "已发送模拟通知"
    }
}

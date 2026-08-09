import AppKit
import Combine
import SwiftUI
import UserNotifications

private enum AppSceneID {
    static let mainWindow = "main-window"
}

private enum AppLaunchMode {
    static var shouldLaunchSilently: Bool {
        CommandLine.arguments.contains("--background")
            || UserDefaults.standard.bool(forKey: "silentLaunch")
    }
}

enum AppWindowLifecycleMode: String, CaseIterable, Identifiable {
    case defaultMode = "default"
    case compatibility = "compatibility"

    static let storageKey = "debugWindowLifecycleMode"

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .defaultMode: return "默认模式"
        case .compatibility: return "兼容模式"
        }
    }
}

enum AppRuntimeConfiguration {
    /// 启动时快照：运行期修改设置只影响下次启动。
    static let requestedWindowLifecycleMode = AppWindowLifecycleMode(
        rawValue: UserDefaults.standard.string(forKey: AppWindowLifecycleMode.storageKey) ?? ""
    ) ?? .defaultMode

    static let usesModernSceneLifecycle: Bool = {
        guard requestedWindowLifecycleMode != .compatibility else { return false }
        if #available(macOS 15.0, *) {
            return true
        }
        return false
    }()

    static var activeWindowLifecycleMode: AppWindowLifecycleMode {
        usesModernSceneLifecycle ? .defaultMode : .compatibility
    }
}

@MainActor
final class MainWindowRequestCenter: ObservableObject {
    static let shared = MainWindowRequestCenter()

    @Published private(set) var generation = 0

    private init() {}

    func requestOpen() {
        generation &+= 1
    }
}

@main
enum DJOneHubNativeApp {
    /// SceneBuilder 无法用 if/else 包装不同可用性的 Scene 修饰器，因此在进入
    /// SwiftUI 生命周期前选择对应的 App 声明。
    @MainActor
    static func main() {
        if #available(macOS 15.0, *), AppRuntimeConfiguration.usesModernSceneLifecycle {
            ModernDJOneHubNativeApp.main()
        } else {
            LegacyDJOneHubNativeApp.main()
        }
    }
}

@MainActor
private final class AppDependencies: ObservableObject {
    let backend: BackendProcess
    let store: DashboardStore
    let smsStore: SMSStore
    let attentionStore: AttentionStore
    let updateChecker: UpdateChecker

    init() {
        let backend = BackendProcess.shared
        self.backend = backend
        store = DashboardStore(backend: backend)
        smsStore = .shared
        attentionStore = .shared
        updateChecker = .shared
    }
}

@available(macOS 15.0, *)
private struct ModernDJOneHubNativeApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var dependencies = AppDependencies()
    private let launchSilently = AppLaunchMode.shouldLaunchSilently

    var body: some Scene {
        Window("DJOneHub", id: AppSceneID.mainWindow) {
            MainAppContent(appDelegate: appDelegate, dependencies: dependencies)
        }
        .windowStyle(.titleBar)
        .restorationBehavior(.disabled)
        .defaultLaunchBehavior(launchSilently ? .suppressed : .presented)

        DJOneHubMenuBarScene(appDelegate: appDelegate, dependencies: dependencies)
    }
}

private struct LegacyDJOneHubNativeApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var dependencies = AppDependencies()

    var body: some Scene {
        Window("DJOneHub", id: AppSceneID.mainWindow) {
            MainAppContent(appDelegate: appDelegate, dependencies: dependencies)
        }
        .windowStyle(.titleBar)

        DJOneHubMenuBarScene(appDelegate: appDelegate, dependencies: dependencies)
    }
}

private struct DJOneHubMenuBarScene: Scene {
    let appDelegate: AppDelegate
    let dependencies: AppDependencies

    var body: some Scene {
        MenuBarExtra {
            MenuBarDashboardPanel(
                appDelegate: appDelegate,
                backend: dependencies.backend,
                store: dependencies.store,
                smsStore: dependencies.smsStore,
                attentionStore: dependencies.attentionStore,
                updateChecker: dependencies.updateChecker)
        } label: {
            MenuBarStatusLabel(
                appDelegate: appDelegate,
                attentionStore: dependencies.attentionStore,
                store: dependencies.store)
        }
        .menuBarExtraStyle(.window)
    }
}

private struct MainAppContent: View {
    let appDelegate: AppDelegate
    let dependencies: AppDependencies

    var body: some View {
        ContentView()
            .environmentObject(dependencies.backend)
            .environmentObject(dependencies.store)
            .environmentObject(dependencies.smsStore)
            .environmentObject(dependencies.attentionStore)
            .environmentObject(dependencies.updateChecker)
            .frame(minWidth: 760, minHeight: 480)
            .onAppear {
                appDelegate.bindDashboardStore(dependencies.store)
            }
    }
}

@MainActor
final class AppDelegate: NSObject, ObservableObject, NSApplicationDelegate,
    UNUserNotificationCenterDelegate, NSWindowDelegate
{
    @Published private(set) var menuBarPresentation = MenuBarPresentation.loading
    private weak var dashboardStore: DashboardStore?
    private var statusCancellables = Set<AnyCancellable>()
    private var previousTrafficSample: TrafficCounterSample?
    private var downloadBytesPerSecond: Double?
    private var uploadBytesPerSecond: Double?
    private var signalImageCache: [Int: NSImage] = [:]
    /// 强引用主窗口，防止关闭（窗口被销毁）后无法重新拉起
    private var mainWindowRef: NSWindow?
    /// macOS 13–14 不支持 Scene 启动抑制，仅在兼容分支中隐藏首个窗口。
    private var legacyBackgroundLaunchHidePending =
        !AppRuntimeConfiguration.usesModernSceneLifecycle
        && AppLaunchMode.shouldLaunchSilently

    func applicationWillFinishLaunching(_ notification: Notification) {
        // 尽早声明后台运行（不显示 Dock 图标；Info.plist 已声明 LSUIElement 兜底）
        NSApp.setActivationPolicy(.accessory)
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        URLProtocol.registerClass(UnixSocketURLProtocol.self)
        // 通知权限按需请求（收到短信时弹出系统授权框），启动时不自动注册
        UNUserNotificationCenter.current().delegate = self
        // 打开应用即自动启动后端，退出应用时自动停止
        BackendProcess.shared.start()
        Task {
            await UpdateChecker.shared.autoCheckIfNeeded()
        }

        // 后台运行：不显示 Dock 图标，窗口关闭后应用保持运行
        NSApp.setActivationPolicy(.accessory)

        // 首次启动默认开启开机自启
        let defaults = UserDefaults.standard
        if !defaults.bool(forKey: "autoLaunchConfigured") {
            defaults.set(true, forKey: "autoLaunchConfigured")
            try? AutoLaunch.setEnabled(true)
        }

        // macOS 15+ 由 SwiftUI Scene 决定是否呈现窗口；普通启动仍需
        // 激活 LSUIElement 窗口。macOS 13–14 的静默启动由下方隐藏逻辑兜底。
        if !AppLaunchMode.shouldLaunchSilently {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                self?.showMainWindow()
            }
        }

        // 捕获主窗口并接管关闭行为（关闭 = 隐藏到后台，窗口实例不销毁）。
        // 系统 reopen 时 SwiftUI 可能新建窗口：新窗口直接提升为新主窗口并接管，
        // 旧窗口自动收起——不会出现"双窗口闪现"，始终只有一个可见窗口。
        NotificationCenter.default.addObserver(
            forName: NSWindow.didBecomeKeyNotification, object: nil, queue: .main
        ) { [weak self] note in
            guard let self, let window = note.object as? NSWindow,
                  !(window is NSPanel), !window.isSheet, window.parent == nil else { return }
            guard let main = self.mainWindowRef else {
                // 首个主窗口：接管并保留
                self.mainWindowRef = window
                window.isReleasedWhenClosed = false
                window.isRestorable = false
                window.delegate = self
                if !AppRuntimeConfiguration.usesModernSceneLifecycle,
                   self.legacyBackgroundLaunchHidePending {
                    self.legacyBackgroundLaunchHidePending = false
                    window.orderOut(nil)
                }
                return
            }
            if main === window {
                window.delegate = self
                return
            }
            // 新出现的窗口（如系统 reopen 时 SwiftUI 新建）：提升为新主窗口，收起旧窗口
            let old = self.mainWindowRef
            self.mainWindowRef = window
            window.isReleasedWhenClosed = false
            window.isRestorable = false
            window.delegate = self
            old?.orderOut(nil)
        }
    }

    // MARK: - 菜单栏状态

    /// 将菜单栏绑定到首页已有的 2 秒状态轮询，不额外请求后端。
    func bindDashboardStore(_ store: DashboardStore) {
        guard dashboardStore !== store else { return }
        dashboardStore = store
        statusCancellables.removeAll()
        resetTrafficRate()

        store.$status
            .receive(on: RunLoop.main)
            .sink { [weak self, weak store] status in
                guard let self else { return }
                self.renderMenuBar(
                    status: status,
                    traffic: store?.traffic,
                    isStale: store?.statusStale ?? false)
            }
            .store(in: &statusCancellables)

        store.$traffic
            .receive(on: RunLoop.main)
            .sink { [weak self, weak store] traffic in
                guard let self else { return }
                self.consumeTrafficSample(traffic)
                self.renderMenuBar(
                    status: store?.status,
                    traffic: traffic,
                    isStale: store?.statusStale ?? false)
            }
            .store(in: &statusCancellables)

        store.$statusStale
            .receive(on: RunLoop.main)
            .sink { [weak self, weak store] isStale in
                guard let self else { return }
                if isStale {
                    self.resetTrafficRate()
                }
                self.renderMenuBar(
                    status: store?.status,
                    traffic: store?.traffic,
                    isStale: isStale)
            }
            .store(in: &statusCancellables)

        NSWorkspace.shared.notificationCenter.publisher(for: NSWorkspace.didWakeNotification)
            .receive(on: RunLoop.main)
            .sink { [weak store] _ in
                Task { @MainActor [weak store] in
                    store?.recoverNetworkAfterWake()
                }
            }
            .store(in: &statusCancellables)

        NotificationCenter.default.publisher(
            for: MenuBarDisplayOptions.didChangeNotification)
            .receive(on: RunLoop.main)
            .sink { [weak self, weak store] _ in
                guard let self else { return }
                self.renderMenuBar(
                    status: store?.status,
                    traffic: store?.traffic,
                    isStale: store?.statusStale ?? false)
            }
            .store(in: &statusCancellables)
    }

    private func consumeTrafficSample(_ snapshot: TrafficSnapshot?) {
        guard let snapshot,
              snapshot.available,
              let rxBytes = snapshot.rxBytes,
              let txBytes = snapshot.txBytes else {
            resetTrafficRate()
            return
        }

        let sampledAt = snapshot.sampledAtMS.map { TimeInterval($0) / 1_000 }
            ?? Date().timeIntervalSince1970
        let current = TrafficCounterSample(
            interfaceName: snapshot.interface,
            rxBytes: rxBytes,
            txBytes: txBytes,
            sampledAt: sampledAt)

        guard let previous = previousTrafficSample else {
            previousTrafficSample = current
            downloadBytesPerSecond = nil
            uploadBytesPerSecond = nil
            return
        }

        // 同一份快照可能因其他状态字段更新而被再次渲染，不应抹掉已算出的速率。
        guard current.sampledAt != previous.sampledAt else { return }

        let elapsed = current.sampledAt - previous.sampledAt
        guard current.interfaceName == previous.interfaceName,
              elapsed > 0,
              current.rxBytes >= previous.rxBytes,
              current.txBytes >= previous.txBytes else {
            previousTrafficSample = current
            downloadBytesPerSecond = nil
            uploadBytesPerSecond = nil
            return
        }

        downloadBytesPerSecond = Double(current.rxBytes - previous.rxBytes) / elapsed
        uploadBytesPerSecond = Double(current.txBytes - previous.txBytes) / elapsed
        previousTrafficSample = current
    }

    private func resetTrafficRate() {
        previousTrafficSample = nil
        downloadBytesPerSecond = nil
        uploadBytesPerSecond = nil
    }

    private func renderMenuBar(
        status: DeviceStatus?, traffic: TrafficSnapshot?, isStale: Bool
    ) {
        let displayOptions = MenuBarDisplayOptions()

        if isStale {
            menuBarPresentation = MenuBarPresentation(
                image: menuBarImage(
                    options: displayOptions,
                    unavailableDescription: "网络状态暂时不可用"),
                rateTitles: displayOptions.rateTitles(download: "—", upload: "—"),
                downloadRate: "—",
                uploadRate: "—",
                networkSummary: "网络状态暂时不可用",
                trafficSummary: "等待下一次刷新…")
            return
        }

        if let hardwareStatus = status?.hardwareStatus, !hardwareStatus.isEmpty {
            resetTrafficRate()
            menuBarPresentation = MenuBarPresentation(
                image: menuBarImage(
                    options: displayOptions,
                    unavailableDescription: "未检测到模块"),
                rateTitles: displayOptions.rateTitles(download: "—", upload: "—"),
                downloadRate: "—",
                uploadRate: "—",
                networkSummary: "未检测到模块",
                trafficSummary: "实时流量不可用")
            return
        }

        let image = menuBarImage(
            options: displayOptions,
            signalDBM: status?.signalDbm,
            unavailableDescription: status == nil ? "正在读取网络信号" : "蜂窝信号暂时不可用")

        let operatorName = status?.operatorName?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let networkMode = status?.networkMode?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let networkParts = [operatorName, networkMode]
            .compactMap { value -> String? in
                guard let value, !value.isEmpty else { return nil }
                return value
            }
        let signalText = status?.signalDbm.map { "\($0) dBm" }
        let summaryParts = networkParts + [signalText].compactMap { $0 }

        let networkSummary: String
        if status?.simInserted == false {
            networkSummary = "SIM 未插入"
        } else if summaryParts.isEmpty {
            networkSummary = status == nil ? "正在读取网络状态…" : "网络状态已连接"
        } else {
            networkSummary = summaryParts.joined(separator: " · ")
        }

        let rateTitles: MenuBarRateTitles
        let downloadRate: String
        let uploadRate: String
        let trafficSummary: String
        if traffic?.available == true {
            let download = formatRate(downloadBytesPerSecond)
            let upload = formatRate(uploadBytesPerSecond)
            downloadRate = download
            uploadRate = upload
            rateTitles = displayOptions.rateTitles(download: download, upload: upload)
            if downloadBytesPerSecond == nil || uploadBytesPerSecond == nil {
                trafficSummary = "正在计算实时流量…"
            } else {
                trafficSummary = "下载 \(download)  ·  上传 \(upload)"
            }
        } else {
            downloadRate = "—"
            uploadRate = "—"
            rateTitles = displayOptions.rateTitles(download: "—", upload: "—")
            trafficSummary = traffic == nil
                ? "实时流量等待采样…" : "实时流量不可用"
        }

        menuBarPresentation = MenuBarPresentation(
            image: image,
            rateTitles: rateTitles,
            downloadRate: downloadRate,
            uploadRate: uploadRate,
            networkSummary: networkSummary,
            trafficSummary: trafficSummary)
    }

    private func menuBarImage(
        options: MenuBarDisplayOptions,
        signalDBM: Int? = nil,
        unavailableDescription: String
    ) -> NSImage? {
        if options.showSignal {
            if let signalDBM {
                return signalImage(for: signalDBM)
            }
            return NSImage(
                systemSymbolName: "exclamationmark.triangle",
                accessibilityDescription: unavailableDescription)
        }

        guard !options.hasSelection else {
            return nil
        }
        return NSImage(
            systemSymbolName: "antenna.radiowaves.left.and.right",
            accessibilityDescription: "DJOneHub")
    }

    private func signalImage(for dbm: Int) -> NSImage {
        let level = signalLevel(for: dbm)
        if let cached = signalImageCache[level] {
            return cached
        }

        let imageWidth = CGFloat(15)
        let barWidth = CGFloat(2.6)
        let barStep = CGFloat(3.7)
        let barsWidth = barStep * 3 + barWidth
        let leadingInset = (imageWidth - barsWidth) / 2
        let image = NSImage(size: NSSize(width: imageWidth, height: 14), flipped: false) { _ in
            for index in 0..<4 {
                let height = CGFloat(4 + index * 3)
                let rect = NSRect(
                    x: leadingInset + CGFloat(index) * barStep,
                    y: 1,
                    width: barWidth,
                    height: height)
                let path = NSBezierPath(
                    roundedRect: rect, xRadius: 0.8, yRadius: 0.8)
                NSColor.black.withAlphaComponent(index < level ? 1 : 0.24).setFill()
                path.fill()
            }
            return true
        }
        image.isTemplate = true
        image.accessibilityDescription = "蜂窝信号，4 格中的 \(level) 格"
        signalImageCache[level] = image
        return image
    }

    private func signalLevel(for dbm: Int) -> Int {
        switch dbm {
        case ..<(-100): return 0
        case ..<(-90): return 1
        case ..<(-80): return 2
        case ..<(-65): return 3
        default: return 4
        }
    }

    private func formatRate(_ bytesPerSecond: Double?) -> String {
        guard let bytesPerSecond,
              bytesPerSecond.isFinite,
              bytesPerSecond >= 0 else { return "—" }

        let units = ["B/s", "KB/s", "MB/s", "GB/s", "TB/s"]
        var value = bytesPerSecond
        var unitIndex = 0
        while value >= 1_024, unitIndex < units.count - 1 {
            value /= 1_024
            unitIndex += 1
        }

        let number: String
        if unitIndex == 0 || value >= 100 {
            number = String(
                format: "%.0f", locale: Locale(identifier: "en_US_POSIX"), value)
        } else if value >= 10 {
            number = String(
                format: "%.1f", locale: Locale(identifier: "en_US_POSIX"), value)
        } else {
            number = String(
                format: "%.2f", locale: Locale(identifier: "en_US_POSIX"), value)
        }
        return "\(number) \(units[unitIndex])"
    }

    func showMainWindow() {
        NSApp.activate(ignoringOtherApps: true)
        guard let window = mainWindowRef ?? NSApp.windows.first(where: { !($0 is NSPanel) }) else { return }
        if window.isMiniaturized {
            window.deminiaturize(nil)
        }
        window.makeKeyAndOrderFront(nil)
    }

    // MARK: - 窗口与生命周期

    /// 关闭窗口（红色按钮）时隐藏到后台，应用继续运行
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        sender.orderOut(nil)
        return false
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationWillTerminate(_ notification: Notification) {
        BackendProcess.shared.stop()
    }

    // MARK: - 通知

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        // App 在前台时也弹出横幅（macOS 默认前台通知静默进通知中心）
        completionHandler([.banner, .sound, .list])
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let userInfo = response.notification.request.content.userInfo
        // 点击短信通知：激活窗口并打开短信页（来电提示已改为应用内自定义卡片，不走系统通知）
        let sender = userInfo["sender"] as? String
        Task { @MainActor in
            MainWindowRequestCenter.shared.requestOpen()
            SMSStore.shared.requestOpenSMS(sender: sender)
        }
        completionHandler()
    }
}

struct MenuBarPresentation {
    let image: NSImage?
    let rateTitles: MenuBarRateTitles
    let downloadRate: String
    let uploadRate: String
    let networkSummary: String
    let trafficSummary: String

    var accessibilitySummary: String {
        "\(networkSummary)，\(trafficSummary)"
    }

    static var loading: MenuBarPresentation {
        let displayOptions = MenuBarDisplayOptions()
        let image: NSImage?
        if displayOptions.showSignal {
            image = NSImage(
                systemSymbolName: "ellipsis",
                accessibilityDescription: "正在读取网络信号")
        } else if displayOptions.hasSelection {
            image = nil
        } else {
            image = NSImage(
                systemSymbolName: "antenna.radiowaves.left.and.right",
                accessibilityDescription: "DJOneHub")
        }

        return MenuBarPresentation(
            image: image,
            rateTitles: displayOptions.rateTitles(download: "—", upload: "—"),
            downloadRate: "—",
            uploadRate: "—",
            networkSummary: "正在读取网络状态…",
            trafficSummary: "实时流量等待采样…")
    }
}

private struct MenuBarStatusLabel: View {
    @Environment(\.openWindow) private var openWindow
    @ObservedObject var appDelegate: AppDelegate
    @ObservedObject private var mainWindowRequests = MainWindowRequestCenter.shared
    @ObservedObject var attentionStore: AttentionStore
    @State private var handledWindowRequest = 0
    let store: DashboardStore

    private var presentation: MenuBarPresentation {
        appDelegate.menuBarPresentation
    }

    private var menuBarBadgeImage: NSImage? {
        guard attentionStore.hasAttention else { return nil }
        return makeMenuBarAttentionBadgeImage(count: attentionStore.totalCount)
    }

    private var accessibilitySummary: String {
        [presentation.accessibilitySummary, attentionStore.accessibilitySummary]
            .compactMap { $0 }
            .joined(separator: "；")
    }

    var body: some View {
        HStack(alignment: .center, spacing: 3.5) {
            if let image = presentation.image {
                Image(nsImage: image)
                    .renderingMode(.template)
                    .resizable()
                    .scaledToFit()
                    .frame(width: 15, height: 14, alignment: .center)
            }
            if let title = presentation.rateTitles.combined {
                Text(title)
                    .font(.caption2)
                    .monospacedDigit()
            }
            if let badgeImage = menuBarBadgeImage {
                Image(nsImage: badgeImage)
                    .renderingMode(.original)
                    .resizable()
                    .interpolation(.high)
                    .frame(width: badgeImage.size.width, height: badgeImage.size.height)
            }
        }
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilitySummary)
        .help(accessibilitySummary)
        .onAppear {
            appDelegate.bindDashboardStore(store)
            handleMainWindowRequest()
        }
        .onChange(of: mainWindowRequests.generation) { _ in
            handleMainWindowRequest()
        }
    }

    private func handleMainWindowRequest() {
        guard handledWindowRequest != mainWindowRequests.generation else { return }
        handledWindowRequest = mainWindowRequests.generation
        NSApp.activate(ignoringOtherApps: true)
        if AppRuntimeConfiguration.usesModernSceneLifecycle {
            openWindow(id: AppSceneID.mainWindow)
        } else {
            appDelegate.showMainWindow()
        }
    }
}

/// 菜单栏标签只保留一个动态 SwiftUI Text；徽标文字预先绘制进 NSImage，
/// 避免 MenuBarExtra 对多个动态 Text 子节点的运行时限制。
@MainActor
private func makeMenuBarAttentionBadgeImage(count: Int) -> NSImage {
    let title = AttentionStore.compactCount(count) as NSString
    let font = NSFont.monospacedDigitSystemFont(ofSize: 9, weight: .semibold)
    let attributes: [NSAttributedString.Key: Any] = [
        .font: font,
        .foregroundColor: NSColor.white,
    ]
    let textSize = title.size(withAttributes: attributes)
    let height: CGFloat = 14
    let width = max(16, ceil(textSize.width) + 8)
    let image = NSImage(size: NSSize(width: width, height: height))
    image.lockFocus()
    NSColor.systemRed.setFill()
    NSBezierPath(
        roundedRect: NSRect(x: 0, y: 1, width: width, height: height - 2),
        xRadius: (height - 2) / 2,
        yRadius: (height - 2) / 2
    ).fill()
    title.draw(
        at: NSPoint(
            x: floor((width - textSize.width) / 2),
            y: floor((height - textSize.height) / 2)),
        withAttributes: attributes)
    image.unlockFocus()
    image.isTemplate = false
    image.accessibilityDescription = "待处理提醒 (count) 个"
    return image
}

private struct MenuBarDashboardPanel: View {
    @Environment(\.openWindow) private var openWindow
    @ObservedObject var appDelegate: AppDelegate
    @ObservedObject var backend: BackendProcess
    @ObservedObject var store: DashboardStore
    @ObservedObject var smsStore: SMSStore
    @ObservedObject var attentionStore: AttentionStore
    @ObservedObject var updateChecker: UpdateChecker

    @AppStorage(MenuBarDisplayOptions.showSignalKey) private var menuBarShowSignal = false
    @AppStorage(MenuBarDisplayOptions.showDownloadKey) private var menuBarShowDownload = false
    @AppStorage(MenuBarDisplayOptions.showUploadKey) private var menuBarShowUpload = false
    @AppStorage("silentLaunch") private var silentLaunchEnabled = false

    @State private var autoLaunchEnabled = false
    @State private var requestingNotificationPermission = false
    @StateObject private var trafficHistory = MenuBarTrafficHistory()
    @StateObject private var routingStore = RoutingStore()

    private var presentation: MenuBarPresentation {
        appDelegate.menuBarPresentation
    }

    var body: some View {
        VStack(spacing: 0) {
            header

            Divider()

            VStack(spacing: 0) {
                trafficSection

                Divider()

                attentionAndRoutingSection

                Divider()

                featureControlsSection
                updateSection
            }
            .padding(.horizontal, 12)

            Divider()

            footer
        }
        .frame(width: 224)
        .onAppear {
            appDelegate.bindDashboardStore(store)
            autoLaunchEnabled = AutoLaunch.isEnabled
            if case .running = backend.state {
                refreshRoutingStatus()
                routingStore.beginPolling()
            }
        }
        .onDisappear {
            routingStore.endPolling()
        }
        .onReceive(store.$traffic) { snapshot in
            trafficHistory.consume(snapshot)
        }
        .onReceive(backend.$state) { state in
            switch state {
            case .running:
                refreshRoutingStatus()
                routingStore.beginPolling()
            default:
                routingStore.endPolling()
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: AutoLaunch.didChangeNotification)) { _ in
            autoLaunchEnabled = AutoLaunch.isEnabled
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(alignment: .center, spacing: 6) {
                Text(networkTitle)
                    .font(.headline.weight(.semibold))
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)

                Spacer(minLength: 8)

                HStack(spacing: 5) {
                    Circle()
                        .fill(connectionTint)
                        .frame(width: 6, height: 6)
                    Text(connectionStatusText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .fixedSize()
                .accessibilityElement(children: .combine)

                Button(store.networkRecovering ? "刷新中…" : "刷新") {
                    store.refresh()
                }
                .buttonStyle(.plain)
                .controlSize(.small)
                .foregroundStyle(.secondary)
                .keyboardShortcut("r", modifiers: .command)
                .disabled(backend.state != .running || store.networkRecovering)
                .help("立即刷新（⌘R）")
                .accessibilityLabel(store.networkRecovering ? "正在恢复网络" : "立即刷新")
            }

            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(networkDetail)
                    .lineLimit(1)
                    .truncationMode(.tail)

                Spacer(minLength: 8)

                Text(communicationSummaryText)
                    .lineLimit(1)
                    .fixedSize()
            }
            .font(.caption)
            .foregroundStyle(.secondary)

            if let connectionDetailText {
                Text(connectionDetailText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .help("\(networkDetail) · \(lastUpdatedCompactText)")
    }

    private var trafficSection: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text("实时流量")
                .font(.callout.weight(.semibold))

            HStack(spacing: 16) {
                trafficRateMetric(
                    title: "下载",
                    value: presentation.downloadRate)
                trafficRateMetric(
                    title: "上传",
                    value: presentation.uploadRate)
            }

            if trafficHistory.samples.isEmpty {
                Text(trafficStatusText ?? "等待流量采样")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                MenuBarTrafficChart(samples: trafficHistory.samples)
            }

            Text(sessionSummaryText)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.8)
        }
        .padding(.vertical, 10)
    }

    private var featureControlsSection: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("功能")
                .font(.callout.weight(.semibold))
                .padding(.bottom, 4)

            controlRow(
                title: "短信通知",
                detail: requestingNotificationPermission
                    ? "正在请求系统通知权限…" : "收到新短信时显示系统通知",
                isOn: notificationsBinding,
                isEnabled: !requestingNotificationPermission)
            controlRow(
                title: "接管短信",
                detail: "保存到本机并清理模块中的原始短信",
                isOn: smsAdoptBinding,
                isEnabled: backend.state == .running)
            controlRow(
                title: store.voiceSwitching ? "语音功能（切换中…）" : "语音功能",
                detail: "启用 USB 音频与通话能力",
                isOn: voiceBinding,
                isEnabled: backend.state == .running && !store.voiceSwitching)

            if let voiceError = store.voiceError, !voiceError.isEmpty {
                Text("语音功能：\(formatMenuText(voiceError, limit: 80))")
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 2)
            }
        }
        .padding(.vertical, 10)
    }

    private var attentionAndRoutingSection: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 6) {
                Text("提醒")
                    .font(.callout.weight(.semibold))

                Spacer(minLength: 6)

                if !attentionStore.hasAttention {
                    Text("无未读")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if attentionStore.unreadSMSCount > 0 {
                    attentionCounter(
                        title: "短信",
                        count: attentionStore.unreadSMSCount,
                        accessibilityName: "未读短信")
                }
                if attentionStore.unviewedCallCount > 0 {
                    attentionCounter(
                        title: "电话",
                        count: attentionStore.unviewedCallCount,
                        accessibilityName: "未查看电话")
                }
            }

            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text("应用分流")
                    .font(.callout.weight(.semibold))

                Spacer(minLength: 6)

                Text(routingStatusText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            if let routingDetailText {
                Text(routingDetailText)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.vertical, 9)
        .help(routingStore.errorMessage ?? routingDetailText ?? routingStatusText)
    }

    private func attentionCounter(
        title: String,
        count: Int,
        accessibilityName: String
    ) -> some View {
        HStack(spacing: 3) {
            Text(title)
                .font(.caption2)
                .foregroundStyle(.secondary)
            AttentionBadge(count: count, accessibilityName: accessibilityName)
        }
        .fixedSize()
    }

    private var routingStatusText: String {
        guard backend.state == .running else { return "服务未运行" }
        if routingStore.isSwitching {
            return routingStore.pendingEnabled == false ? "正在停用" : "正在启用"
        }
        switch routingStore.loadPhase {
        case .idle, .loading:
            return "读取中"
        case .failed:
            return "不可用"
        case .loaded:
            guard routingStore.runtime.enabled else { return "未启用" }
            return routingStore.runtime.mode?.title ?? routingStore.config.mode.title
        }
    }

    private var routingDetailText: String? {
        guard backend.state == .running,
              routingStore.isLoaded,
              routingStore.runtime.enabled else { return nil }
        switch routingStore.runtime.mode ?? routingStore.config.mode {
        case .independent:
            let rules = routingStore.config.applications.count
            return rules > 0
                ? "\(rules) 个规则 · 默认 \(routingStore.config.defaultAction.title)"
                : "默认 \(routingStore.config.defaultAction.title)"
        case .clash:
            return routingStore.runtime.socksAddress
                ?? "SOCKS5 127.0.0.1:\(routingStore.config.clashListenPort)"
        }
    }

    private func refreshRoutingStatus() {
        if routingStore.isLoaded {
            Task { await routingStore.refreshRuntime() }
        } else {
            routingStore.load()
        }
    }

    @ViewBuilder
    private var updateSection: some View {
        if let release = updateChecker.pendingUpdate {
            Divider()

            HStack(spacing: 8) {
                Text("新版本 \(release.tagName) 可用")
                    .font(.callout)
                    .lineLimit(1)

                Spacer(minLength: 8)

                Button("下载") {
                    if let url = updateChecker.downloadURL {
                        NSWorkspace.shared.open(url)
                    }
                    updateChecker.dismissUpdate()
                }
                .controlSize(.small)
                .disabled(updateChecker.downloadURL == nil)
            }
            .padding(.vertical, 8)
        }
    }

    private var footer: some View {
        HStack(spacing: 10) {
            Button {
                showMainWindow()
            } label: {
                Text("打开主界面")
                    .foregroundStyle(.primary)
            }
            .buttonStyle(.plain)

            Spacer(minLength: 8)

            Menu {
                Toggle("登录时启动", isOn: autoLaunchBinding)
                Toggle("静默启动", isOn: $silentLaunchEnabled)

                Divider()

                Toggle("菜单栏显示信号", isOn: signalDisplayBinding)
                Toggle("菜单栏显示下载速率", isOn: downloadDisplayBinding)
                Toggle("菜单栏显示上传速率", isOn: uploadDisplayBinding)

                Divider()

                Button("通知设置…") {
                    openNotificationSettings()
                }
            } label: {
                Text("设置")
                    .foregroundStyle(.secondary)
            }
            .menuStyle(.borderlessButton)
            .fixedSize()
            .help("启动方式、菜单栏显示与系统通知设置")

            Button {
                NSApp.terminate(nil)
            } label: {
                Text("退出")
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
            .keyboardShortcut("q", modifiers: .command)
        }
        .controlSize(.small)
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private func trafficRateMetric(
        title: String,
        value: String
    ) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title)
                .font(.caption2)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title3.weight(.medium).monospacedDigit())
                .lineLimit(1)
                .minimumScaleFactor(0.75)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
    }

    private func controlRow(
        title: String,
        detail: String,
        isOn: Binding<Bool>,
        isEnabled: Bool = true
    ) -> some View {
        HStack(spacing: 8) {
            Text(title)
                .font(.callout)
                .lineLimit(1)

            Spacer(minLength: 8)

            Toggle("", isOn: isOn)
                .labelsHidden()
                .toggleStyle(.switch)
                .controlSize(.mini)
                .accessibilityLabel(title)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .frame(minHeight: 28)
        .disabled(!isEnabled)
        .help(detail)
    }

    private var networkTitle: String {
        if let hardwareStatus = store.status?.hardwareStatus, !hardwareStatus.isEmpty {
            return "未检测到模块"
        }
        if let operatorName = store.status?.operatorName?
            .trimmingCharacters(in: .whitespacesAndNewlines),
           !operatorName.isEmpty
        {
            return operatorName
        }
        return "DJOneHub"
    }

    private var networkDetail: String {
        let values = [
            store.status?.networkMode,
            store.status?.signalDbm.map { "\($0) dBm" },
            store.status?.radioBand,
        ]
        let parts = values.compactMap { value -> String? in
            guard let value = value?.trimmingCharacters(in: .whitespacesAndNewlines),
                  !value.isEmpty else { return nil }
            return value
        }
        return parts.isEmpty ? presentation.networkSummary : parts.joined(separator: " · ")
    }

    private var connectionStatusText: String {
        switch backend.state {
        case .running:
            if store.statusStale { return "数据过期" }
            guard let status = store.status else { return "正在读取" }
            if status.simInserted == false { return "SIM 未插入" }
            if let hardwareStatus = status.hardwareStatus, !hardwareStatus.isEmpty {
                return "模块未连接"
            }
            return "已连接"
        case .starting:
            return "正在启动"
        case .stopped:
            return "已停止"
        case .failed:
            return "连接失败"
        }
    }

    private var connectionTint: Color {
        switch backend.state {
        case .running:
            if store.statusStale { return .orange }
            guard let status = store.status else { return .orange }
            if status.simInserted == false { return .orange }
            if let hardwareStatus = status.hardwareStatus, !hardwareStatus.isEmpty {
                return .orange
            }
            return .green
        case .starting:
            return .orange
        case .stopped:
            return Color(nsColor: .secondaryLabelColor)
        case .failed:
            return .red
        }
    }

    private var autoLaunchBinding: Binding<Bool> {
        Binding(
            get: { autoLaunchEnabled },
            set: { setAutoLaunch($0) })
    }

    private var notificationsBinding: Binding<Bool> {
        Binding(
            get: { smsStore.notificationsEnabled },
            set: { setNotificationsEnabled($0) })
    }

    private var smsAdoptBinding: Binding<Bool> {
        Binding(
            get: { store.smsAdopt },
            set: { enabled in
                if enabled {
                    guard confirmSystemAction(
                        title: "开启短信接管？",
                        message: "收到的短信将保存到本机，并自动清理 SIM 卡与模块中的原始短信。",
                        confirmTitle: "开启短信接管"
                    ) else { return }
                }
                store.setSMSAdopt(enabled)
            })
    }

    private var voiceBinding: Binding<Bool> {
        Binding(
            get: { store.voiceEnabled },
            set: { enabled in
                guard confirmSystemAction(
                    title: enabled ? "启用语音功能？" : "关闭语音功能？",
                    message: "切换语音功能会让模块短暂重启，期间网络和通话暂时不可用。",
                    confirmTitle: enabled ? "启用并重启模块" : "关闭并重启模块"
                ) else { return }
                store.setVoiceEnabled(enabled)
            })
    }

    private var signalDisplayBinding: Binding<Bool> {
        Binding(
            get: { menuBarShowSignal },
            set: {
                menuBarShowSignal = $0
                notifyMenuBarDisplayOptionsChanged()
            })
    }

    private var downloadDisplayBinding: Binding<Bool> {
        Binding(
            get: { menuBarShowDownload },
            set: {
                menuBarShowDownload = $0
                notifyMenuBarDisplayOptionsChanged()
            })
    }

    private var uploadDisplayBinding: Binding<Bool> {
        Binding(
            get: { menuBarShowUpload },
            set: {
                menuBarShowUpload = $0
                notifyMenuBarDisplayOptionsChanged()
            })
    }

    private var connectionDetailText: String? {
        if case .failed(let reason) = backend.state, !reason.isEmpty {
            return formatMenuText(reason)
        }
        if store.statusStale {
            return "暂时无法读取模块状态，当前显示上一次数据。"
        }
        if let hardwareStatus = store.status?.hardwareStatus, !hardwareStatus.isEmpty {
            return formatMenuText(hardwareStatus)
        }
        return nil
    }

    private var trafficStatusText: String? {
        if store.traffic?.available == false {
            let message = store.traffic?.error ?? "实时流量暂时不可用"
            return formatMenuText(message)
        }
        return nil
    }

    private var communicationSummaryText: String {
        let smsSummary = "\(smsStore.items.count) 条短信"
        switch store.callStatus.state {
        case "incoming", "active", "dialing", "alerting":
            return "\(callStatusText) · \(smsSummary)"
        default:
            return smsSummary
        }
    }

    private var sessionSummaryText: String {
        let download = trafficMetric(store.traffic?.sessionRX)
        let upload = trafficMetric(store.traffic?.sessionTX)
        return "本次  下载 \(download) · 上传 \(upload)"
    }

    private var callStatusText: String {
        switch store.callStatus.state {
        case "incoming": return "来电"
        case "active": return "通话中"
        case "dialing": return "拨号中"
        case "alerting": return "呼叫中"
        case "unknown": return "状态未知"
        default: return "空闲"
        }
    }

    private var lastUpdatedCompactText: String {
        guard let lastUpdated = store.lastUpdated else { return "等待更新" }
        return "更新 \(lastUpdated.formatted(date: .omitted, time: .standard))"
    }

    private func trafficMetric(_ bytes: UInt64?) -> String {
        guard store.traffic?.available == true, let bytes else { return "—" }
        let formatter = ByteCountFormatter()
        formatter.countStyle = .binary
        formatter.allowedUnits = [.useKB, .useMB, .useGB, .useTB]
        return formatter.string(fromByteCount: Int64(clamping: bytes))
    }

    private func formatMenuText(_ text: String, limit: Int = 48) -> String {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.count > limit else { return trimmed }
        return String(trimmed.prefix(limit)) + "…"
    }

    private func setAutoLaunch(_ enabled: Bool) {
        do {
            try AutoLaunch.setEnabled(enabled)
            autoLaunchEnabled = enabled
        } catch {
            autoLaunchEnabled = AutoLaunch.isEnabled
            showSystemError(
                title: "无法修改开机自启",
                message: error.localizedDescription)
        }
    }

    private func setNotificationsEnabled(_ enabled: Bool) {
        guard enabled else {
            smsStore.notificationsEnabled = false
            return
        }

        requestingNotificationPermission = true
        Task {
            let granted = await smsStore.ensureAuthorization()
            requestingNotificationPermission = false
            smsStore.notificationsEnabled = granted

            if !granted {
                showNotificationPermissionAlert()
            }
        }
    }

    private func confirmSystemAction(
        title: String,
        message: String,
        confirmTitle: String
    ) -> Bool {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = message
        alert.addButton(withTitle: confirmTitle)
        alert.addButton(withTitle: "取消")
        return alert.runModal() == .alertFirstButtonReturn
    }

    private func showSystemError(title: String, message: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = message
        alert.addButton(withTitle: "好")
        alert.runModal()
    }

    private func showNotificationPermissionAlert() {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "通知权限未开启"
        alert.informativeText = "请在系统设置中允许 DJOneHub 发送通知。"
        alert.addButton(withTitle: "打开通知设置")
        alert.addButton(withTitle: "取消")
        if alert.runModal() == .alertFirstButtonReturn {
            openNotificationSettings()
        }
    }

    private func notifyMenuBarDisplayOptionsChanged() {
        NotificationCenter.default.post(
            name: MenuBarDisplayOptions.didChangeNotification,
            object: nil)
    }

    private func showMainWindow() {
        NSApp.activate(ignoringOtherApps: true)
        if AppRuntimeConfiguration.usesModernSceneLifecycle {
            openWindow(id: AppSceneID.mainWindow)
        } else {
            appDelegate.showMainWindow()
        }
    }

    private func openNotificationSettings() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension") {
            NSWorkspace.shared.open(url)
        }
    }
}

private struct MenuBarTrafficChartSample: Identifiable {
    let id = UUID()
    let download: Double
    let upload: Double
}

private final class MenuBarTrafficHistory: ObservableObject {
    @Published private(set) var samples: [MenuBarTrafficChartSample] = []

    private struct CounterSample {
        let interfaceName: String?
        let rxBytes: UInt64
        let txBytes: UInt64
        let sampledAt: TimeInterval
    }

    private var previous: CounterSample?
    private let capacity = 48

    func consume(_ snapshot: TrafficSnapshot?) {
        guard let snapshot,
              snapshot.available,
              let rxBytes = snapshot.rxBytes,
              let txBytes = snapshot.txBytes else {
            reset()
            return
        }

        let current = CounterSample(
            interfaceName: snapshot.interface,
            rxBytes: rxBytes,
            txBytes: txBytes,
            sampledAt: snapshot.sampledAtMS.map { TimeInterval($0) / 1_000 }
                ?? Date().timeIntervalSince1970)

        guard let previous else {
            self.previous = current
            return
        }

        guard current.sampledAt != previous.sampledAt else { return }

        let elapsed = current.sampledAt - previous.sampledAt
        guard current.interfaceName == previous.interfaceName,
              elapsed > 0,
              elapsed < 120,
              current.rxBytes >= previous.rxBytes,
              current.txBytes >= previous.txBytes else {
            self.previous = current
            samples.removeAll()
            return
        }

        let sample = MenuBarTrafficChartSample(
            download: Double(current.rxBytes - previous.rxBytes) / elapsed,
            upload: Double(current.txBytes - previous.txBytes) / elapsed)
        self.previous = current
        samples.append(sample)
        if samples.count > capacity {
            samples.removeFirst(samples.count - capacity)
        }
    }

    private func reset() {
        previous = nil
        if !samples.isEmpty {
            samples.removeAll()
        }
    }
}

private struct MenuBarTrafficChart: View {
    let samples: [MenuBarTrafficChartSample]

    var body: some View {
        Canvas { context, size in
            let slotWidth = size.width / 48
            let barWidth = max(1, slotWidth - 2)
            let startX = size.width - slotWidth * CGFloat(samples.count)
            let maximum = max(
                1,
                samples.reduce(0) { result, sample in
                    max(result, sample.download + sample.upload)
                })

            var baseline = Path()
            baseline.move(to: CGPoint(x: 0, y: size.height - 0.5))
            baseline.addLine(to: CGPoint(x: size.width, y: size.height - 0.5))
            context.stroke(
                baseline,
                with: .color(Color(nsColor: .separatorColor)),
                lineWidth: 0.5)

            for (index, sample) in samples.enumerated() {
                let x = startX + CGFloat(index) * slotWidth
                let total = sample.download + sample.upload
                let height = total > 0
                    ? max(1, CGFloat(total / maximum) * (size.height - 3)) : 0

                guard height > 0 else { continue }

                var bar = Path()
                bar.addRoundedRect(
                    in: CGRect(
                        x: x,
                        y: size.height - height,
                        width: barWidth,
                        height: height),
                    cornerSize: CGSize(width: 1.5, height: 1.5))
                context.fill(
                    bar,
                    with: .color(Color.primary.opacity(0.42)))
            }
        }
        .frame(height: 28)
        .accessibilityHidden(true)
    }
}

struct MenuBarDisplayOptions {
    static let showSignalKey = "menuBarShowSignal"
    static let showDownloadKey = "menuBarShowDownloadRate"
    static let showUploadKey = "menuBarShowUploadRate"
    static let didChangeNotification = Notification.Name("menuBarDisplayOptionsDidChange")

    let showSignal: Bool
    let showDownload: Bool
    let showUpload: Bool

    init(defaults: UserDefaults = .standard) {
        showSignal = defaults.bool(forKey: Self.showSignalKey)
        showDownload = defaults.bool(forKey: Self.showDownloadKey)
        showUpload = defaults.bool(forKey: Self.showUploadKey)
    }

    var hasSelection: Bool {
        showSignal || showDownload || showUpload
    }

    func rateTitles(download: String, upload: String) -> MenuBarRateTitles {
        MenuBarRateTitles(
            download: showDownload ? "↓\(download)" : nil,
            upload: showUpload ? "↑\(upload)" : nil)
    }
}

struct MenuBarRateTitles {
    let download: String?
    let upload: String?

    var combined: String? {
        switch (download, upload) {
        case let (download?, upload?):
            // U+2004 在 macOS caption2 字体下约 3.45pt，与图文间距保持一致。
            return "\(download)\u{2004}\(upload)"
        case let (download?, nil):
            return download
        case let (nil, upload?):
            return upload
        case (nil, nil):
            return nil
        }
    }
}

private struct TrafficCounterSample {
    let interfaceName: String?
    let rxBytes: UInt64
    let txBytes: UInt64
    let sampledAt: TimeInterval
}

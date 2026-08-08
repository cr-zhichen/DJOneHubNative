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
    let updateChecker: UpdateChecker

    init() {
        let backend = BackendProcess.shared
        self.backend = backend
        store = DashboardStore(backend: backend)
        smsStore = .shared
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
            MenuBarStatusMenu(appDelegate: appDelegate)
        } label: {
            MenuBarStatusLabel(
                appDelegate: appDelegate,
                store: dependencies.store)
        }
        .menuBarExtraStyle(.menu)
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
        let trafficSummary: String
        if traffic?.available == true {
            let download = formatRate(downloadBytesPerSecond)
            let upload = formatRate(uploadBytesPerSecond)
            rateTitles = displayOptions.rateTitles(download: download, upload: upload)
            if downloadBytesPerSecond == nil || uploadBytesPerSecond == nil {
                trafficSummary = "正在计算实时流量…"
            } else {
                trafficSummary = "下载 \(download)  ·  上传 \(upload)"
            }
        } else {
            rateTitles = displayOptions.rateTitles(download: "—", upload: "—")
            trafficSummary = traffic == nil
                ? "实时流量等待采样…" : "实时流量不可用"
        }

        menuBarPresentation = MenuBarPresentation(
            image: image,
            rateTitles: rateTitles,
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
            networkSummary: "正在读取网络状态…",
            trafficSummary: "实时流量等待采样…")
    }
}

private struct MenuBarStatusLabel: View {
    @Environment(\.openWindow) private var openWindow
    @ObservedObject var appDelegate: AppDelegate
    @ObservedObject private var mainWindowRequests = MainWindowRequestCenter.shared
    @State private var handledWindowRequest = 0
    let store: DashboardStore

    private var presentation: MenuBarPresentation {
        appDelegate.menuBarPresentation
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
        }
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(presentation.accessibilitySummary)
        .help(presentation.accessibilitySummary)
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

private struct MenuBarStatusMenu: View {
    @Environment(\.openWindow) private var openWindow
    @ObservedObject var appDelegate: AppDelegate

    private var presentation: MenuBarPresentation {
        appDelegate.menuBarPresentation
    }

    var body: some View {
        Text(presentation.networkSummary)
        Text(presentation.trafficSummary)

        Divider()

        Button("显示主界面") {
            NSApp.activate(ignoringOtherApps: true)
            if AppRuntimeConfiguration.usesModernSceneLifecycle {
                openWindow(id: AppSceneID.mainWindow)
            } else {
                appDelegate.showMainWindow()
            }
        }

        Divider()

        Button("退出 DJOneHub") {
            NSApp.terminate(nil)
        }
        .keyboardShortcut("q", modifiers: .command)
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

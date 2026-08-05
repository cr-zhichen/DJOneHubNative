import SwiftUI
import AppKit
import UserNotifications

@main
struct DJOneHubNativeApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var backend = BackendProcess.shared
    @StateObject private var store = DashboardStore(backend: BackendProcess.shared)
    @StateObject private var smsStore = SMSStore.shared
    @StateObject private var updateChecker = UpdateChecker.shared

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(backend)
                .environmentObject(store)
                .environmentObject(smsStore)
                .environmentObject(updateChecker)
                .frame(minWidth: 760, minHeight: 480)
                .task {
                    await updateChecker.autoCheckIfNeeded()
                }
        }
        .windowStyle(.titleBar)
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate, NSWindowDelegate {
    private var statusItem: NSStatusItem?
    /// 强引用主窗口，防止关闭（窗口被销毁）后无法重新拉起
    private var mainWindowRef: NSWindow?
    /// 静默启动（开机自启 --background 或用户开启"静默启动"）：首个窗口出现即隐藏
    private var backgroundLaunchHidePending = CommandLine.arguments.contains("--background")
        || UserDefaults.standard.bool(forKey: "silentLaunch")

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

        // 后台运行：不显示 Dock 图标，窗口关闭后应用保持运行
        NSApp.setActivationPolicy(.accessory)
        setupStatusItem()

        // 首次启动默认开启开机自启
        let defaults = UserDefaults.standard
        if !defaults.bool(forKey: "autoLaunchConfigured") {
            defaults.set(true, forKey: "autoLaunchConfigured")
            try? AutoLaunch.setEnabled(true)
        }

        // 正常启动（非静默）：激活应用并把主窗口置前。
        // LSUIElement 应用启动时系统不会自动激活，窗口会停留在其他窗口后面；
        // 同时兜底"退出时窗口隐藏 → 恢复出隐藏窗口"导致的启动后无窗口。
        if !backgroundLaunchHidePending {
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
                if self.backgroundLaunchHidePending {
                    self.backgroundLaunchHidePending = false
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

    // MARK: - 菜单栏图标

    private func setupStatusItem() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let button = item.button {
            button.image = NSImage(systemSymbolName: "antenna.radiowaves.left.and.right", accessibilityDescription: "DJOneHub")
        }
        let menu = NSMenu()
        menu.addItem(withTitle: "显示主界面", action: #selector(showMainWindow), keyEquivalent: "")
        menu.addItem(NSMenuItem.separator())
        let quitItem = NSMenuItem(title: "退出 DJOneHub", action: #selector(quitApp), keyEquivalent: "q")
        quitItem.keyEquivalentModifierMask = [.command]
        menu.addItem(quitItem)
        // 左右键点击均弹出同一菜单
        item.menu = menu
        statusItem = item
    }

    @objc private func showMainWindow() {
        NSApp.activate(ignoringOtherApps: true)
        guard let window = mainWindowRef ?? NSApp.windows.first(where: { !($0 is NSPanel) }) else { return }
        if window.isMiniaturized {
            window.deminiaturize(nil)
        }
        window.makeKeyAndOrderFront(nil)
    }

    @objc private func quitApp() {
        NSApp.terminate(nil)
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
        NSApp.activate(ignoringOtherApps: true)
        let sender = userInfo["sender"] as? String
        Task { @MainActor in
            SMSStore.shared.requestOpenSMS(sender: sender)
        }
        completionHandler()
    }
}

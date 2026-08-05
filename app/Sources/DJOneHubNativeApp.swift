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

final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        URLProtocol.registerClass(UnixSocketURLProtocol.self)
        // 通知权限按需请求（收到短信时弹出系统授权框），启动时不自动注册
        UNUserNotificationCenter.current().delegate = self
        // 打开应用即自动启动后端，退出应用时自动停止
        BackendProcess.shared.start()
    }

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

    func applicationWillTerminate(_ notification: Notification) {
        BackendProcess.shared.stop()
    }
}

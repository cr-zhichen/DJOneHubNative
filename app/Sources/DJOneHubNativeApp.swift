import SwiftUI
import AppKit
import UserNotifications

@main
struct DJOneHubNativeApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var backend = BackendProcess.shared
    @StateObject private var store = DashboardStore(backend: BackendProcess.shared)
    @StateObject private var smsStore = SMSStore.shared

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(backend)
                .environmentObject(store)
                .environmentObject(smsStore)
                .frame(minWidth: 760, minHeight: 480)
        }
        .windowStyle(.titleBar)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        URLProtocol.registerClass(UnixSocketURLProtocol.self)
        // 通知权限按需请求（收到短信/用户开启时），启动时不自动注册
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
        // 点击短信通知：激活窗口并打开短信页
        NSApp.activate(ignoringOtherApps: true)
        let sender = response.notification.request.content.userInfo["sender"] as? String
        Task { @MainActor in
            SMSStore.shared.requestOpenSMS(sender: sender)
        }
        completionHandler()
    }

    func applicationWillTerminate(_ notification: Notification) {
        BackendProcess.shared.stop()
    }
}

import SwiftUI
import AppKit

@main
struct DJOneHubNativeApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var backend = BackendProcess.shared
    @StateObject private var store = DashboardStore(backend: BackendProcess.shared)

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(backend)
                .environmentObject(store)
                .frame(minWidth: 760, minHeight: 480)
        }
        .windowStyle(.titleBar)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        URLProtocol.registerClass(UnixSocketURLProtocol.self)
        // 打开应用即自动启动后端，退出应用时自动停止
        BackendProcess.shared.start()
    }

    func applicationWillTerminate(_ notification: Notification) {
        BackendProcess.shared.stop()
    }
}

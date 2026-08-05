import AppKit
import Darwin
import ServiceManagement

/// 开机自启管理：优先使用 SMAppService（需应用位于 /Applications），
/// 失败时回退到写入 ~/Library/LaunchAgents plist 的方式。
@MainActor
enum AutoLaunch {
    static let launchAgentLabel = "com.djonehub.native"
    static let launchAgentPath = NSHomeDirectory() + "/Library/LaunchAgents/" + launchAgentLabel + ".plist"

    /// 当前是否已启用自启
    static var isEnabled: Bool {
        if SMAppService.mainApp.status == .enabled {
            return true
        }
        return FileManager.default.fileExists(atPath: launchAgentPath)
    }

    static func setEnabled(_ enabled: Bool) throws {
        if enabled {
            try enable()
        } else {
            try disable()
        }
    }

    private static func enable() throws {
        // 优先 SMAppService
        if SMAppService.mainApp.status != .enabled {
            do {
                try SMAppService.mainApp.register()
                if SMAppService.mainApp.status == .enabled {
                    return
                }
            } catch {
                // 回退到 LaunchAgent
            }
        }
        try installLaunchAgent()
    }

    private static func disable() throws {
        if SMAppService.mainApp.status == .enabled {
            try? SMAppService.mainApp.unregister()
        }
        try uninstallLaunchAgent()
    }

    private static func installLaunchAgent() throws {
        guard let executable = Bundle.main.executablePath else {
            throw AutoLaunchError.failed("无法获取应用可执行文件路径")
        }
        let plist: [String: Any] = [
            "Label": launchAgentLabel,
            // --background：登录时后台运行，不弹出主窗口
            "ProgramArguments": [executable, "--background"],
            "RunAtLoad": true,
        ]
        let data = try PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0)
        try data.write(to: URL(fileURLWithPath: launchAgentPath), options: .atomic)

        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        process.arguments = ["bootstrap", "gui/\(getuid())", launchAgentPath]
        try process.run()
        process.waitUntilExit()
        if process.terminationStatus != 0 {
            throw AutoLaunchError.failed("launchctl bootstrap 失败（\(process.terminationStatus)）")
        }
    }

    private static func uninstallLaunchAgent() throws {
        if FileManager.default.fileExists(atPath: launchAgentPath) {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
            process.arguments = ["bootout", "gui/\(getuid())", launchAgentPath]
            try? process.run()
            process.waitUntilExit()
            try? FileManager.default.removeItem(atPath: launchAgentPath)
        }
    }
}

enum AutoLaunchError: LocalizedError {
    case failed(String)

    var errorDescription: String? {
        switch self {
        case .failed(let message): return message
        }
    }
}

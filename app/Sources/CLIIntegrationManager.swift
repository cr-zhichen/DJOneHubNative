import AppKit
import CryptoKit
import Darwin
import Foundation
import Security

struct CLIPermission: Identifiable, Hashable {
    let id: String
    let title: String
    let detail: String
    let systemImage: String
    let mutating: Bool
    let prerequisite: String?

    static let all: [CLIPermission] = [
        .init(
            id: "device.read", title: "读取设备状态",
            detail: "读取模块、SIM、信号、运营商与注册状态。",
            systemImage: "antenna.radiowaves.left.and.right", mutating: false, prerequisite: nil),
        .init(
            id: "network.read", title: "读取网络状态",
            detail: "读取网络诊断与流量计数。",
            systemImage: "network", mutating: false, prerequisite: nil),
        .init(
            id: "sms.read", title: "读取短信",
            detail: "先列出元数据，再按稳定 ID 读取短信正文。",
            systemImage: "message", mutating: false, prerequisite: nil),
        .init(
            id: "sms.send", title: "发送短信",
            detail: "允许 AI 向明确号码发送短信；执行时仍需 --yes。",
            systemImage: "paperplane", mutating: true, prerequisite: "sms.read"),
        .init(
            id: "call.read", title: "读取通话状态",
            detail: "读取当前通话状态、事件与本机通话记录。",
            systemImage: "phone", mutating: false, prerequisite: nil),
        .init(
            id: "call.dial", title: "拨打电话",
            detail: "允许 AI 发起普通语音呼叫；执行时仍需 --yes。",
            systemImage: "phone.arrow.up.right", mutating: true, prerequisite: "call.read"),
        .init(
            id: "call.answer", title: "接听电话",
            detail: "只在后端确认存在来电时接听；执行时仍需 --yes。",
            systemImage: "phone.down", mutating: true, prerequisite: "call.read"),
        .init(
            id: "call.hangup", title: "挂断电话",
            detail: "保留为安全退出动作，不额外要求 --yes。",
            systemImage: "phone.down.fill", mutating: true, prerequisite: "call.read"),
        .init(
            id: "esim.read", title: "读取 eSIM",
            detail: "读取卡片、Profile 与异步操作状态。",
            systemImage: "simcard", mutating: false, prerequisite: nil),
        .init(
            id: "esim.switch", title: "切换 eSIM",
            detail: "允许切换到已安装 Profile；会触发模块重连且仍需 --yes。",
            systemImage: "arrow.triangle.2.circlepath", mutating: true, prerequisite: "esim.read"),
    ]

    static let readOnlyScopes = Set(all.filter { !$0.mutating }.map(\.id))
    static let communicationScopes = Set(all.map(\.id))
}

enum CLIPermissionPreset: String {
    case readOnly
    case communication
    case custom
}

enum CLIInstallationState: Equatable {
    case checking
    case bundledUnavailable
    case notInstalled
    case installed
    case updateAvailable
    case conflict(String)
}

@MainActor
final class CLIIntegrationManager: ObservableObject {
    static let destinationURL = URL(fileURLWithPath: "/usr/local/bin/djonehub")
    static let markerURL = URL(fileURLWithPath: "/usr/local/share/djonehub/managed.json")

    @Published private(set) var installationState: CLIInstallationState = .checking
    @Published private(set) var installedCLIVersion: String?
    @Published private(set) var isWorking = false
    @Published private(set) var accessEnabled = false
    @Published private(set) var grantedScopes = CLIPermission.communicationScopes
    @Published var errorMessage: String?
    @Published var operationMessage: String?

    private var accessToken = CLIIntegrationManager.makeToken()
    private let fileManager = FileManager.default

    var bundledCLIURL: URL? {
        Bundle.main.url(forResource: "djonehub", withExtension: nil, subdirectory: "backend")
    }

    var currentVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
    }

    var bundledCLIVersion: String { currentVersion }

    var cliUpdateAvailable: Bool {
        installationState == .updateAvailable
    }

    var bundledCLIVersionText: String {
        Self.displayVersion(bundledCLIVersion)
    }

    var installedCLIVersionText: String? {
        installedCLIVersion.map(Self.displayVersion)
    }

    var accessConfigURL: URL {
        supportDirectory.appendingPathComponent("cli-access.json")
    }

    var preset: CLIPermissionPreset {
        if grantedScopes == CLIPermission.readOnlyScopes { return .readOnly }
        if grantedScopes == CLIPermission.communicationScopes { return .communication }
        return .custom
    }

    var copyablePrompt: String {
        """
        DJOneHub 已安装在这台 Mac 上。请运行：
        djonehub agent skill export --format json

        读取返回结果中的 entrypoint_content、source_directory 和 files，并按照当前 Agent 支持的方式安装 Skill。如果当前 Agent 不支持安装 Skill，请直接读取并遵循 entrypoint_content。

        完成后，先运行 djonehub capabilities 确认可用能力。
        """
    }

    init() {
        loadAccessConfiguration()
        ensureAccessConfigurationExists()
        refreshInstallationState()
        saveRuntimeMetadata()
    }

    func refreshInstallationState() {
        let inspection = inspectInstallation()
        installedCLIVersion = inspection.installedVersion
        installationState = inspection.state
    }

    func copyPrompt() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(copyablePrompt, forType: .string)
        operationMessage = "已复制 Skill 导出提示词"
    }

    func setAccessEnabled(_ enabled: Bool) {
        let oldEnabled = accessEnabled
        let oldToken = accessToken
        accessEnabled = enabled
        accessToken = Self.makeToken()
        do {
            try saveAccessConfiguration()
            operationMessage = enabled ? "AI 访问已启用，新凭证已生效" : "AI 访问已关闭，旧凭证已撤销"
            errorMessage = nil
        } catch {
            accessEnabled = oldEnabled
            accessToken = oldToken
            errorMessage = error.localizedDescription
        }
    }

    func setPermission(_ permission: CLIPermission, enabled: Bool) {
        let oldScopes = grantedScopes
        if enabled {
            grantedScopes.insert(permission.id)
            if let prerequisite = permission.prerequisite {
                grantedScopes.insert(prerequisite)
            }
        } else {
            grantedScopes.remove(permission.id)
            for dependent in CLIPermission.all where dependent.prerequisite == permission.id {
                grantedScopes.remove(dependent.id)
            }
        }
        do {
            try saveAccessConfiguration()
            operationMessage = "AI 权限已更新"
            errorMessage = nil
        } catch {
            grantedScopes = oldScopes
            errorMessage = error.localizedDescription
        }
    }

    func applyPreset(_ preset: CLIPermissionPreset) {
        let oldScopes = grantedScopes
        switch preset {
        case .readOnly:
            grantedScopes = CLIPermission.readOnlyScopes
        case .communication:
            grantedScopes = CLIPermission.communicationScopes
        case .custom:
            return
        }
        do {
            try saveAccessConfiguration()
            operationMessage = preset == .readOnly ? "已应用只读预设" : "已应用通信助手预设"
            errorMessage = nil
        } catch {
            grantedScopes = oldScopes
            errorMessage = error.localizedDescription
        }
    }

    func installCLI() {
        guard !isWorking else { return }
        guard let sourceURL = bundledCLIURL else {
            installationState = .bundledUnavailable
            return
        }
        isWorking = true
        errorMessage = nil
        operationMessage = nil
        Task {
            do {
                let sourceHash = try Self.sha256(of: sourceURL)
                let marker = CLIInstallationMarker(
                    product: "DJOneHub",
                    bundleIdentifier: Bundle.main.bundleIdentifier ?? "com.djonehub.native",
                    version: currentVersion,
                    installedSHA256: sourceHash,
                    installedAt: ISO8601DateFormatter().string(from: Date()))
                let encoder = JSONEncoder()
                encoder.outputFormatting = [.sortedKeys]
                let markerData = try encoder.encode(marker)
                try await Self.runPrivileged(shellCommand: Self.installShell(
                    source: sourceURL.path,
                    destination: Self.destinationURL.path,
                    marker: Self.markerURL.path,
                    markerData: markerData))
                refreshInstallationState()
                operationMessage = "CLI 已安装到 /usr/local/bin/djonehub"
            } catch {
                refreshInstallationState()
                errorMessage = error.localizedDescription
            }
            isWorking = false
        }
    }

    func uninstallCLI() {
        guard !isWorking else { return }
        isWorking = true
        errorMessage = nil
        operationMessage = nil
        Task {
            do {
                try await Self.runPrivileged(shellCommand: Self.uninstallShell(
                    destination: Self.destinationURL.path,
                    marker: Self.markerURL.path))
                setAccessEnabled(false)
                refreshInstallationState()
                operationMessage = "CLI 已卸载，AI 访问凭证已撤销"
            } catch {
                refreshInstallationState()
                errorMessage = error.localizedDescription
            }
            isWorking = false
        }
    }

    private var supportDirectory: URL {
        fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
            .appendingPathComponent("DJOneHubNative", isDirectory: true)
    }

    private var runtimeMetadataURL: URL {
        supportDirectory.appendingPathComponent("cli-runtime.json")
    }

    private func loadAccessConfiguration() {
        guard fileManager.fileExists(atPath: accessConfigURL.path) else { return }
        do {
            let attributes = try fileManager.attributesOfItem(atPath: accessConfigURL.path)
            if let permissions = attributes[.posixPermissions] as? NSNumber,
               permissions.intValue & 0o077 != 0 {
                throw CLIIntegrationError("AI 权限文件不是仅当前用户可读，请重新保存配置。")
            }
            let data = try Data(contentsOf: accessConfigURL)
            let config = try JSONDecoder().decode(CLIAccessConfiguration.self, from: data)
            guard config.version == 1, config.token.count >= 32 else {
                throw CLIIntegrationError("AI 权限配置版本无效。")
            }
            accessEnabled = config.enabled
            accessToken = config.token
            grantedScopes = Set(config.scopes).intersection(Set(CLIPermission.all.map(\.id)))
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func ensureAccessConfigurationExists() {
        guard !fileManager.fileExists(atPath: accessConfigURL.path) else { return }
        do {
            try saveAccessConfiguration()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func saveAccessConfiguration() throws {
        try prepareSupportDirectory()
        let config = CLIAccessConfiguration(
            version: 1,
            enabled: accessEnabled,
            token: accessToken,
            scopes: grantedScopes.sorted(),
            updatedAt: ISO8601DateFormatter().string(from: Date()))
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let data = try encoder.encode(config)
        try data.write(to: accessConfigURL, options: .atomic)
        guard Darwin.chmod(accessConfigURL.path, 0o600) == 0 else {
            throw CLIIntegrationError("无法将 AI 权限文件设为仅当前用户可读。")
        }
    }

    private func saveRuntimeMetadata() {
        guard let bundledCLIURL else {
            try? fileManager.removeItem(at: runtimeMetadataURL)
            return
        }
        do {
            try prepareSupportDirectory()
            let metadata = CLIRuntimeMetadata(
                version: 1,
                appVersion: currentVersion,
                bundledCLIVersion: bundledCLIVersion,
                bundledCLISHA256: try Self.sha256(of: bundledCLIURL),
                appBundlePath: Bundle.main.bundleURL.path,
                updatedAt: ISO8601DateFormatter().string(from: Date()))
            let encoder = JSONEncoder()
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            try encoder.encode(metadata).write(to: runtimeMetadataURL, options: .atomic)
            guard Darwin.chmod(runtimeMetadataURL.path, 0o600) == 0 else {
                throw CLIIntegrationError("无法保护 CLI 运行时版本文件。")
            }
        } catch {
            if errorMessage == nil {
                errorMessage = "无法更新 CLI 版本提示：\(error.localizedDescription)"
            }
        }
    }

    private func prepareSupportDirectory() throws {
        try fileManager.createDirectory(at: supportDirectory, withIntermediateDirectories: true)
        guard Darwin.chmod(supportDirectory.path, 0o700) == 0 else {
            throw CLIIntegrationError("无法保护 DJOneHub 的本机配置目录。")
        }
    }

    private func inspectInstallation() -> CLIInstallationInspection {
        guard let sourceURL = bundledCLIURL,
              fileManager.fileExists(atPath: sourceURL.path) else {
            return .init(state: .bundledUnavailable, installedVersion: nil)
        }
        var fileStatus = stat()
        guard Darwin.lstat(Self.destinationURL.path, &fileStatus) == 0 else {
            let state: CLIInstallationState = errno == ENOENT
                ? .notInstalled
                : .conflict("无法检查 /usr/local/bin/djonehub。")
            return .init(state: state, installedVersion: nil)
        }
        let fileType = fileStatus.st_mode & S_IFMT
        guard fileType != S_IFLNK else {
            return .init(
                state: .conflict("已存在同名符号链接（包括空引用）；不会覆盖。请先手动确认其来源。"),
                installedVersion: nil)
        }
        guard fileType == S_IFREG else {
            return .init(
                state: .conflict("/usr/local/bin/djonehub 不是由 DJOneHub 管理的普通文件。"),
                installedVersion: nil)
        }
        do {
            var markerStatus = stat()
            guard Darwin.lstat(Self.markerURL.path, &markerStatus) == 0 else {
                return .init(
                    state: .conflict("已存在同名 CLI，但缺少 DJOneHub 管理标记；不会覆盖。"),
                    installedVersion: nil)
            }
            guard (markerStatus.st_mode & S_IFMT) == S_IFREG else {
                return .init(
                    state: .conflict("DJOneHub CLI 管理标记不是普通文件；不会覆盖。"),
                    installedVersion: nil)
            }
            let markerData = try Data(contentsOf: Self.markerURL)
            let marker = try JSONDecoder().decode(CLIInstallationMarker.self, from: markerData)
            guard marker.product == "DJOneHub",
                  Self.isDJOneHubBundleIdentifier(marker.bundleIdentifier) else {
                return .init(
                    state: .conflict("现有 CLI 的管理标记不属于 DJOneHub。"),
                    installedVersion: nil)
            }
            let installedHash = try Self.sha256(of: Self.destinationURL)
            guard installedHash == marker.installedSHA256 else {
                return .init(
                    state: .conflict("现有 CLI 已被修改；为避免删除其他文件，请手动检查。"),
                    installedVersion: nil)
            }
            let bundledHash = try Self.sha256(of: sourceURL)
            let state: CLIInstallationState = bundledHash == installedHash
                ? .installed
                : .updateAvailable
            return .init(state: state, installedVersion: marker.version)
        } catch {
            return .init(
                state: .conflict("无法验证现有 CLI：\(error.localizedDescription)"),
                installedVersion: nil)
        }
    }

    private static func isDJOneHubBundleIdentifier(_ identifier: String) -> Bool {
        let stableIdentifier = "com.djonehub.native"
        return identifier == stableIdentifier || identifier.hasPrefix(stableIdentifier + ".")
    }

    private static func displayVersion(_ version: String) -> String {
        guard version != "dev" else { return "开发构建" }
        return version.hasPrefix("v") ? version : "v\(version)"
    }

    private static func makeToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            return UUID().uuidString.replacingOccurrences(of: "-", with: "")
                + UUID().uuidString.replacingOccurrences(of: "-", with: "")
        }
        return bytes.map { String(format: "%02x", $0) }.joined()
    }

    private static func sha256(of url: URL) throws -> String {
        let data = try Data(contentsOf: url, options: .mappedIfSafe)
        return SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    private static func installShell(
        source: String,
        destination: String,
        marker: String,
        markerData: Data
    ) -> String {
        let source = shellQuote(source)
        let destination = shellQuote(destination)
        let marker = shellQuote(marker)
        let markerPayload = shellQuote(markerData.base64EncodedString())
        let destinationTemp = shellQuote(Self.destinationURL.path + ".djonehub-new")
        let markerTemp = shellQuote(Self.markerURL.path + ".djonehub-new")
        return [
            "set -eu",
            "if [ -L \(destination) ]; then exit 40; fi",
            "if [ -L \(marker) ]; then exit 43; fi",
            "if [ -e \(destination) ]; then [ -f \(marker) ] || exit 41; recorded=$(/usr/bin/plutil -extract installed_sha256 raw -o - \(marker) 2>/dev/null || true); actual=$(/usr/bin/shasum -a 256 \(destination) | /usr/bin/awk '{print $1}'); [ \"$recorded\" = \"$actual\" ] || exit 42; fi",
            "/usr/bin/install -d -m 0755 /usr/local/bin /usr/local/share/djonehub",
            "/bin/rm -f \(destinationTemp) \(markerTemp)",
            "/usr/bin/install -m 0755 \(source) \(destinationTemp)",
            "/usr/bin/printf %s \(markerPayload) | /usr/bin/base64 -D > \(markerTemp)",
            "/bin/chmod 0644 \(markerTemp)",
            "/bin/mv -f \(destinationTemp) \(destination)",
            "/bin/mv -f \(markerTemp) \(marker)",
        ].joined(separator: "; ")
    }

    private static func uninstallShell(destination: String, marker: String) -> String {
        let destination = shellQuote(destination)
        let marker = shellQuote(marker)
        return [
            "set -eu",
            "[ ! -L \(destination) ] || exit 50",
            "[ ! -L \(marker) ] || exit 54",
            "[ -f \(destination) ] || exit 51",
            "[ -f \(marker) ] || exit 52",
            "recorded=$(/usr/bin/plutil -extract installed_sha256 raw -o - \(marker) 2>/dev/null || true)",
            "actual=$(/usr/bin/shasum -a 256 \(destination) | /usr/bin/awk '{print $1}')",
            "[ \"$recorded\" = \"$actual\" ] || exit 53",
            "/bin/rm -f \(destination) \(marker)",
            "/bin/rmdir /usr/local/share/djonehub 2>/dev/null || true",
        ].joined(separator: "; ")
    }

    private static func shellQuote(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    nonisolated private static func appleScriptLiteral(_ value: String) -> String {
        "\"" + value
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
            .replacingOccurrences(of: "\n", with: "\\n") + "\""
    }

    nonisolated private static func runPrivileged(shellCommand: String) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            DispatchQueue.global(qos: .userInitiated).async {
                let process = Process()
                let pipe = Pipe()
                process.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
                process.arguments = [
                    "-e",
                    "do shell script \(appleScriptLiteral(shellCommand)) with administrator privileges",
                ]
                process.standardOutput = pipe
                process.standardError = pipe
                do {
                    try process.run()
                    process.waitUntilExit()
                    let output = String(
                        data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8
                    )?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    if process.terminationStatus == 0 {
                        continuation.resume()
                    } else {
                        let message = output.isEmpty
                            ? "管理员操作未完成（状态 \(process.terminationStatus)）。"
                            : output
                        continuation.resume(throwing: CLIIntegrationError(message))
                    }
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }
}

private struct CLIAccessConfiguration: Codable {
    let version: Int
    let enabled: Bool
    let token: String
    let scopes: [String]
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case version, enabled, token, scopes
        case updatedAt = "updated_at"
    }
}

private struct CLIInstallationMarker: Codable {
    let product: String
    let bundleIdentifier: String
    let version: String
    let installedSHA256: String
    let installedAt: String

    enum CodingKeys: String, CodingKey {
        case product, version
        case bundleIdentifier = "bundle_identifier"
        case installedSHA256 = "installed_sha256"
        case installedAt = "installed_at"
    }
}

private struct CLIRuntimeMetadata: Codable {
    let version: Int
    let appVersion: String
    let bundledCLIVersion: String
    let bundledCLISHA256: String
    let appBundlePath: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case version
        case appVersion = "app_version"
        case bundledCLIVersion = "bundled_cli_version"
        case bundledCLISHA256 = "bundled_cli_sha256"
        case appBundlePath = "app_bundle_path"
        case updatedAt = "updated_at"
    }
}

private struct CLIInstallationInspection {
    let state: CLIInstallationState
    let installedVersion: String?
}

private struct CLIIntegrationError: LocalizedError {
    let message: String

    init(_ message: String) {
        self.message = message
    }

    var errorDescription: String? { message }
}

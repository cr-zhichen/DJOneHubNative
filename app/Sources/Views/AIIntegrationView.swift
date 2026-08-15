import AppKit
import SwiftUI

/// AI 与 CLI：集中管理正式 CLI、后端强制权限和软件内置 Skill。
struct AIIntegrationView: View {
    @EnvironmentObject private var manager: CLIIntegrationManager
    @State private var confirmation: AIIntegrationConfirmation?

    var body: some View {
        Form {
            Section {
                Text("为本机 AI 提供稳定、可审计的 JSON 命令接口。CLI 只访问你在此页授权的能力，软件内置 Skill 负责告诉 AI 如何安全调用。")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Section("命令行工具") {
                LabeledContent("安装状态") {
                    Label(installationTitle, systemImage: installationIcon)
                        .foregroundStyle(installationColor)
                }

                LabeledContent("App 内置 CLI") {
                    Text(manager.bundledCLIVersionText)
                        .font(.callout.monospacedDigit())
                        .textSelection(.enabled)
                }

                LabeledContent("已安装 CLI") {
                    Text(installedVersionText)
                        .font(.callout.monospacedDigit())
                        .foregroundStyle(manager.installedCLIVersion == nil ? Color.secondary : Color.primary)
                        .textSelection(.enabled)
                }

                LabeledContent("安装位置") {
                    Text("/usr/local/bin/djonehub")
                        .font(.callout.monospaced())
                        .textSelection(.enabled)
                }

                Text("安装或卸载 CLI 时，需要管理员授权。")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                Text(installationDetail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                HStack(spacing: 8) {
                    if canInstall {
                        Button(installButtonTitle) {
                            manager.installCLI()
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(manager.isWorking)
                    }

                    if canUninstall {
                        Button("卸载 CLI", role: .destructive) {
                            manager.uninstallCLI()
                        }
                        .disabled(manager.isWorking)
                    }

                    if manager.isWorking {
                        ProgressView()
                            .controlSize(.small)
                            .accessibilityLabel("正在更新 CLI")
                    }
                }
            }

            Section("AI 访问") {
                Toggle("允许本机 AI 通过 CLI 访问", isOn: accessBinding)

                Text(manager.accessEnabled
                     ? "访问令牌只保存在当前用户的应用数据目录；关闭开关会立即轮换并撤销旧令牌。"
                     : "CLI 可以保持安装，但后端会拒绝所有 AI 请求。")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                HStack(spacing: 8) {
                    Text("权限预设")
                    Spacer()
                    Button {
                        manager.applyPreset(.readOnly)
                    } label: {
                        presetLabel("只读", selected: manager.preset == .readOnly)
                    }
                    .buttonStyle(.bordered)
                    Button {
                        if manager.accessEnabled {
                            confirmation = .communicationPreset
                        } else {
                            manager.applyPreset(.communication)
                        }
                    } label: {
                        presetLabel("通信助手", selected: manager.preset == .communication)
                    }
                    .buttonStyle(.bordered)
                    if manager.preset == .custom {
                        Text("自定义")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                ForEach(CLIPermission.all) { permission in
                    permissionRow(permission)
                }
            }

            Section("软件内置 Skill") {
                Label("Skill 已内置，无需额外下载", systemImage: "shippingbox.fill")
                    .font(.callout)

                Text("把下面的提示词交给当前 Agent。CLI 会提供 Skill 的完整内容，由 Agent 根据自身能力选择安装或直接使用。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                Text(manager.copyablePrompt)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(nsColor: .textBackgroundColor))
                    .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                    .overlay {
                        RoundedRectangle(cornerRadius: 8, style: .continuous)
                            .stroke(Color(nsColor: .separatorColor), lineWidth: 1)
                    }

                Button {
                    manager.copyPrompt()
                } label: {
                    Label("复制给 Agent", systemImage: "doc.on.doc")
                }
            }

            if let message = manager.operationMessage {
                Section {
                    Label(message, systemImage: "checkmark.circle.fill")
                        .font(.callout)
                        .foregroundStyle(.green)
                }
            }

            if let error = manager.errorMessage {
                Section {
                    Label {
                        Text(error)
                            .fixedSize(horizontal: false, vertical: true)
                    } icon: {
                        Image(systemName: "exclamationmark.triangle.fill")
                    }
                    .font(.callout)
                    .foregroundStyle(.red)
                }
            }
        }
        .formStyle(.grouped)
        .frame(maxWidth: 640)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .navigationTitle("AI 与 CLI")
        .onAppear {
            manager.refreshInstallationState()
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            manager.refreshInstallationState()
        }
        .alert(item: $confirmation, content: confirmationAlert)
    }

    private var accessBinding: Binding<Bool> {
        Binding(
            get: { manager.accessEnabled },
            set: { enabled in
                if enabled {
                    confirmation = .enableAccess
                } else {
                    manager.setAccessEnabled(false)
                }
            })
    }

    private func permissionRow(_ permission: CLIPermission) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: permission.systemImage)
                .frame(width: 20)
                .foregroundStyle(permission.mutating ? Color.orange : Color.secondary)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(permission.title)
                    if permission.mutating {
                        Text("会执行操作")
                            .font(.caption2)
                            .foregroundStyle(.orange)
                    }
                }
                Text(permission.detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 12)
            Toggle("", isOn: permissionBinding(permission))
                .labelsHidden()
                .accessibilityLabel(permission.title)
        }
        .padding(.vertical, 2)
    }

    private func presetLabel(_ title: String, selected: Bool) -> some View {
        HStack(spacing: 4) {
            if selected {
                Image(systemName: "checkmark")
            }
            Text(title)
        }
    }

    private func permissionBinding(_ permission: CLIPermission) -> Binding<Bool> {
        Binding(
            get: { manager.grantedScopes.contains(permission.id) },
            set: { enabled in
                if enabled, permission.mutating, manager.accessEnabled {
                    confirmation = .grant(permission)
                } else {
                    manager.setPermission(permission, enabled: enabled)
                }
            })
    }

    private func confirmationAlert(_ item: AIIntegrationConfirmation) -> Alert {
        switch item {
        case .enableAccess:
            let actions = CLIPermission.all
                .filter { $0.mutating && manager.grantedScopes.contains($0.id) }
                .map(\.title)
                .joined(separator: "、")
            let detail = actions.isEmpty
                ? "当前仅授权读取能力。"
                : "当前还允许这些操作：\(actions)。CLI 执行高影响操作时仍要求明确确认。"
            return Alert(
                title: Text("启用 AI 访问？"),
                message: Text(detail),
                primaryButton: .default(Text("启用")) { manager.setAccessEnabled(true) },
                secondaryButton: .cancel())
        case .grant(let permission):
            return Alert(
                title: Text("授权“\(permission.title)”？"),
                message: Text(permission.detail),
                primaryButton: .default(Text("授权")) {
                    manager.setPermission(permission, enabled: true)
                },
                secondaryButton: .cancel())
        case .communicationPreset:
            return Alert(
                title: Text("应用通信助手预设？"),
                message: Text("将授权读取状态、读取和发送短信、读取和控制通话，以及读取和切换已安装 eSIM。"),
                primaryButton: .default(Text("应用预设")) {
                    manager.applyPreset(.communication)
                },
                secondaryButton: .cancel())
        }
    }

    private var installationTitle: String {
        switch manager.installationState {
        case .checking: return "正在检查"
        case .bundledUnavailable: return "安装包缺少 CLI"
        case .notInstalled: return "未安装"
        case .installed: return "已安装最新版本"
        case .updateAvailable: return "版本不同"
        case .conflict: return "发现同名文件"
        }
    }

    private var installationIcon: String {
        switch manager.installationState {
        case .checking: return "clock"
        case .bundledUnavailable, .conflict: return "exclamationmark.triangle.fill"
        case .notInstalled: return "terminal"
        case .installed: return "checkmark.circle.fill"
        case .updateAvailable: return "arrow.down.circle.fill"
        }
    }

    private var installationColor: Color {
        switch manager.installationState {
        case .installed: return .green
        case .bundledUnavailable, .conflict: return .orange
        default: return .secondary
        }
    }

    private var installationDetail: String {
        switch manager.installationState {
        case .checking:
            return "正在检查 CLI 状态。"
        case .bundledUnavailable:
            return "当前 App 中未找到 CLI，请重新安装 DJOneHub。"
        case .notInstalled:
            return "安装后，可在终端和支持命令行工具的 Agent 中使用 djonehub。"
        case .installed:
            return "CLI 已可用。DJOneHub 更新后，如需同步新版本，会在这里提醒你。"
        case .updateAvailable:
            return "已安装版本与当前 App 不一致，请同步后继续使用。"
        case .conflict(let reason):
            return reason
        }
    }

    private var canInstall: Bool {
        switch manager.installationState {
        case .notInstalled, .updateAvailable: return true
        default: return false
        }
    }

    private var canUninstall: Bool {
        switch manager.installationState {
        case .installed, .updateAvailable: return true
        default: return false
        }
    }

    private var installButtonTitle: String {
        manager.installationState == .updateAvailable ? "同步 CLI" : "安装 CLI"
    }

    private var installedVersionText: String {
        if let version = manager.installedCLIVersion {
            return manager.installedCLIVersionText ?? version
        }
        switch manager.installationState {
        case .checking:
            return "正在检查"
        case .notInstalled:
            return "未安装"
        case .conflict:
            return "未知（未受管理）"
        case .bundledUnavailable, .installed, .updateAvailable:
            return "未知"
        }
    }

}

private enum AIIntegrationConfirmation: Identifiable {
    case enableAccess
    case grant(CLIPermission)
    case communicationPreset

    var id: String {
        switch self {
        case .enableAccess: return "enable-access"
        case .grant(let permission): return "grant-\(permission.id)"
        case .communicationPreset: return "communication-preset"
        }
    }
}

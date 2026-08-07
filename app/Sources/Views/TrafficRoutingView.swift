import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// 应用分流是显式启用的会话级能力：配置会保存，运行状态不会跨应用重启恢复。
struct TrafficRoutingView: View {
    @StateObject private var store = RoutingStore()
    @State private var copiedClashConfig = false
    @State private var showingUninstallConfirmation = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header

                switch store.loadPhase {
                case .idle, .loading:
                    loadingState
                case .failed(let message):
                    loadFailureState(message)
                case .loaded:
                    modeAndReadiness
                    if store.config.mode == .independent {
                        independentConfiguration
                    } else {
                        clashManagedConfiguration
                    }
                }
            }
            .padding(18)
            .frame(maxWidth: 900, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .top)
        }
        .navigationTitle("应用分流")
        .onAppear {
            store.load()
            store.beginPolling()
        }
        .onDisappear {
            store.endPolling()
        }
        .alert("操作未完成", isPresented: Binding(
            get: { store.errorMessage != nil },
            set: { if !$0 { store.errorMessage = nil } }
        )) {
            Button("好", role: .cancel) { store.errorMessage = nil }
        } message: {
            Text(store.errorMessage ?? "未知错误")
        }
        .confirmationDialog(
            "卸载 TUN 权限服务？",
            isPresented: $showingUninstallConfirmation,
            titleVisibility: .visible
        ) {
            Button("卸载服务", role: .destructive) {
                store.uninstallService()
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text(uninstallConfirmationMessage)
        }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text("应用分流")
                    .font(.title2.bold())
                Text("选择默认出口、指定应用走 4G，或把 4G 出口交给 Clash 管理。功能默认关闭。")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 20)
            VStack(alignment: .trailing, spacing: 3) {
                HStack(spacing: 8) {
                    if store.isSwitching {
                        ProgressView()
                            .controlSize(.small)
                            .accessibilityLabel(store.pendingEnabled == false ? "正在停用" : "正在启用")
                    }
                    Toggle("启用", isOn: Binding(
                        get: { store.toggleIsOn },
                        set: { store.setEnabled($0) }
                    ))
                    .toggleStyle(.switch)
                    .disabled(toggleDisabled)
                    .accessibilityHint(toggleHint)
                }
                if let reason = store.enableBlockReason {
                    Text(reason)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.horizontal, 2)
    }

    private var toggleDisabled: Bool {
        store.isSwitching || store.isUninstalling || store.isSaving
            || (!store.runtime.enabled && !store.canEnable)
    }

    private var toggleHint: String {
        if let reason = store.enableBlockReason { return reason }
        if store.config.mode == .independent {
            if store.capabilities?.serviceCurrent == true {
                return "启用已安装的 TUN 权限服务"
            }
            if store.capabilities?.serviceInstalled == true {
                return "启用时需要管理员授权并更新 TUN 权限服务"
            }
            return "首次启用时需要管理员授权并安装 TUN 权限服务"
        }
        return "启用本地 4G SOCKS5 出口"
    }

    private var loadingState: some View {
        HStack(spacing: 10) {
            ProgressView().controlSize(.small)
            Text("读取分流配置…")
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, minHeight: 180)
    }

    private func loadFailureState(_ message: String) -> some View {
        VStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 26))
                .foregroundStyle(.orange)
                .accessibilityHidden(true)
            Text("无法读取分流配置")
                .font(.headline)
            Text(message)
                .font(.callout)
                .foregroundStyle(.primary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 520)
            Button("重试") { store.load() }
                .buttonStyle(.borderedProminent)
        }
        .frame(maxWidth: .infinity, minHeight: 220)
        .routingPanel()
    }

    private var modeAndReadiness: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .center, spacing: 12) {
                Picker("运行模式", selection: $store.config.mode) {
                    ForEach(RoutingMode.allCases) { mode in
                        Text(mode.title).tag(mode)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 360, alignment: .leading)
                .disabled(store.isConfigurationLocked)

                Spacer()

                runtimeBadge

                Button {
                    Task { await store.checkPreflight() }
                } label: {
                    Label("重新检测", systemImage: "arrow.clockwise")
                }
                .controlSize(.small)
                .disabled(store.isChecking || store.isSwitching)
            }

            Divider()

            readinessRows

            if shouldShowRoutingServiceControl {
                Divider()
                routingServiceControl
            }

            if let preflight = store.preflight {
                ForEach(preflight.issues, id: \.self) { issue in
                    noticeRow(issue, color: .red, icon: "xmark.octagon.fill")
                }
                ForEach(preflight.conflicts) { conflict in
                    noticeRow(conflict.detail + "。请关闭 Clash 等工具的 TUN 模式后重试。",
                              color: .orange, icon: "exclamationmark.triangle.fill")
                }
                ForEach(preflight.warnings, id: \.self) { warning in
                    noticeRow(warning, color: .orange, icon: "exclamationmark.triangle")
                }
            }

            if store.runtime.state == "failed", let message = store.runtime.message, !message.isEmpty {
                noticeRow(message, color: .red, icon: "xmark.octagon.fill")
            }

            HStack {
                Text("配置会保存，但每次启动 DJOneHub 后仍保持关闭，需手动启用。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                if store.isDirty {
                    HStack(spacing: 4) {
                        Image(systemName: "exclamationmark.circle.fill")
                            .foregroundStyle(.orange)
                            .accessibilityHidden(true)
                        Text("有未保存的修改")
                            .foregroundStyle(.primary)
                    }
                    .font(.caption.bold())
                }
                Button(store.isSaving ? "正在保存…" : "保存配置") {
                    Task { _ = await store.save() }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .disabled(!store.isDirty || store.isSaving || store.isConfigurationLocked)
            }
        }
        .routingPanel()
    }

    private var shouldShowRoutingServiceControl: Bool {
        store.config.mode == .independent || store.capabilities?.serviceInstalled == true
    }

    private var routingServiceControl: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: "lock.shield")
                .foregroundStyle(routingServiceColor)
                .frame(width: 18)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                Text("TUN 权限服务")
                    .font(.callout.bold())
                Text(routingServiceDescription)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer(minLength: 16)

            if store.capabilities?.serviceInstalled == true {
                if store.isUninstalling {
                    ProgressView()
                        .controlSize(.small)
                        .accessibilityLabel("正在卸载 TUN 权限服务")
                }
                Button("卸载…", role: .destructive) {
                    showingUninstallConfirmation = true
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(store.isUninstalling || store.isSwitching || store.isSaving)
                .accessibilityHint("移除特权服务；下次开启独立分流时需要重新授权")
            } else {
                Text("未安装")
                    .font(.caption.bold())
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var routingServiceDescription: String {
        if store.capabilities?.serviceCurrent == true {
            return "已安装。关闭分流只停止 TUN；卸载后下次开启需要重新授权。"
        }
        if store.capabilities?.serviceInstalled == true {
            return "需要更新。下次开启独立分流时会请求管理员授权并更新服务。"
        }
        return "首次开启独立分流时自动安装，并请求管理员授权。"
    }

    private var routingServiceColor: Color {
        if store.capabilities?.serviceCurrent == true { return .green }
        if store.capabilities?.serviceInstalled == true { return .orange }
        return .secondary
    }

    private var uninstallConfirmationMessage: String {
        if store.runtime.enabled && store.runtime.mode == .independent {
            return "需要管理员授权。当前独立分流会先停止，然后移除特权服务和网络核心副本。分流配置会保留。"
        }
        return "需要管理员授权。将移除特权服务和网络核心副本。分流配置会保留，下次开启时需要重新授权。"
    }

    @ViewBuilder
    private var readinessRows: some View {
        let module = store.preflight?.moduleInterface ?? store.runtime.moduleInterface
        let systemInterface = store.preflight?.systemInterface ?? store.runtime.systemInterface
        VStack(spacing: 8) {
            readinessRow(
                title: "4G 模块",
                value: module.map(routingModuleDescription) ?? "未检测到",
                icon: "antenna.radiowaves.left.and.right",
                color: module == nil ? .red : .green)

            if store.config.mode == .independent {
                readinessRow(
                    title: "系统出口",
                    value: systemInterface?.isEmpty == false ? systemInterface! : "未检测到",
                    icon: "network",
                    color: systemInterface?.isEmpty == false ? .green : .red)
            } else {
                readinessRow(
                    title: "本地 SOCKS5",
                    value: "127.0.0.1:\(store.config.clashListenPort)",
                    icon: "arrow.left.arrow.right",
                    color: store.runtime.enabled ? .green : .secondary)
            }

            readinessRow(
                title: "网络核心",
                value: coreDescription,
                icon: "cpu",
                color: store.capabilities?.coreAvailable == true ? .green : .red)
        }
    }

    private var independentConfiguration: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .center, spacing: 16) {
                VStack(alignment: .leading, spacing: 3) {
                    Text("默认出口")
                        .font(.headline)
                    Text("没有单独规则的应用使用此出口。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Spacer(minLength: 16)

                Picker("默认出口", selection: $store.config.defaultAction) {
                    ForEach(RoutingAction.defaultExitCases) { action in
                        Label(action.title, systemImage: action.icon).tag(action)
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .frame(width: 165)
                .disabled(store.isConfigurationLocked)
                .accessibilityHint("未设置单独规则的应用将使用这个出口")
            }

            Divider().padding(.vertical, 2)

            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 3) {
                    Text("应用出口规则")
                        .font(.headline)
                    Text("已添加的应用会覆盖默认出口；4G 直连不会经过任何 SOCKS。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button {
                    chooseApplications()
                } label: {
                    Label("添加应用", systemImage: "plus")
                }
                .controlSize(.small)
                .disabled(store.isConfigurationLocked)
            }

            if store.config.applications.isEmpty {
                emptyApplications
            } else {
                VStack(spacing: 0) {
                    ForEach(Array(store.config.applications.enumerated()), id: \.element.id) { index, application in
                        applicationRow(application)
                        if index < store.config.applications.count - 1 {
                            Divider().padding(.leading, 48)
                        }
                    }
                }
            }

            if store.usesSystemSOCKS {
                Divider().padding(.vertical, 2)
                systemSOCKSFields
            }

            Divider().padding(.vertical, 2)
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "lock.shield")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Text("独立分流会创建唯一的 TUN。检测到 Clash、Mihomo 等正在接管大范围路由时，DJOneHub 会拒绝启动；4G 或 SOCKS 不可用时，对应应用不会自动回落到系统直连。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .routingPanel()
    }

    private var clashManagedConfiguration: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 3) {
                Text("4G SOCKS5 出口")
                    .font(.headline)
                Text("DJOneHub 不创建 TUN，也不配置应用规则；Clash 负责全部应用分流。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 10) {
                Text("监听端口")
                    .foregroundStyle(.secondary)
                    .frame(width: 72, alignment: .leading)
                TextField("端口", value: $store.config.clashListenPort, format: .number.grouping(.never))
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 110)
                    .disabled(store.isConfigurationLocked)
                Text(verbatim: "127.0.0.1:\(String(store.config.clashListenPort))")
                    .font(.system(.body, design: .monospaced))
                    .textSelection(.enabled)
                Spacer()
            }

            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text("Clash 代理配置")
                        .font(.callout.bold())
                    Spacer()
                    Button(copiedClashConfig ? "已复制" : "复制配置") {
                        store.copyClashConfiguration()
                        copiedClashConfig = true
                        Task {
                            try? await Task.sleep(nanoseconds: 1_500_000_000)
                            copiedClashConfig = false
                        }
                    }
                    .controlSize(.small)
                }
                Text(store.clashConfigurationYAML)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 7))
            }

            Divider()

            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "point.3.connected.trianglepath.dotted")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 4) {
                    Text("在 Clash 中把目标应用指向 DJOneHub-4G")
                        .font(.callout.bold())
                    Text("流量路径：Clash TUN → 本地 SOCKS5 → DJOneHub → Baiwang 4G 网卡。此模式允许 Clash TUN 保持开启。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .routingPanel()
    }

    private var systemSOCKSFields: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 3) {
                Text("系统侧 SOCKS5")
                    .font(.callout.bold())
                Text("可作为默认出口，也可用于单独标记的应用；到 SOCKS 服务的连接由系统网络处理。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack(spacing: 10) {
                TextField("服务器", text: $store.config.systemSOCKS.server)
                    .textFieldStyle(.roundedBorder)
                    .frame(minWidth: 170)
                TextField("端口", value: $store.config.systemSOCKS.port, format: .number.grouping(.never))
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 90)
                TextField("用户名（可选）", text: $store.config.systemSOCKS.username)
                    .textFieldStyle(.roundedBorder)
                    .frame(minWidth: 130)
                SecureField("密码（可选）", text: $store.config.systemSOCKS.password)
                    .textFieldStyle(.roundedBorder)
                    .frame(minWidth: 130)
            }
            .disabled(store.isConfigurationLocked)

            if store.config.defaultAction == .systemSOCKS {
                Label(
                    "使用本机 SOCKS 时会自动绕过 TUN；端口未监听或无法识别进程时不会启动。",
                    systemImage: "arrow.uturn.backward.circle"
                )
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private var emptyApplications: some View {
        VStack(spacing: 8) {
            Image(systemName: "square.stack.3d.up.slash")
                .font(.system(size: 28))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
            Text("尚未添加应用")
                .font(.callout.bold())
            Text("所有应用当前使用默认出口：\(store.config.defaultAction.title)。")
                .font(.caption)
                .foregroundStyle(.secondary)
            Button("选择应用…") { chooseApplications() }
                .controlSize(.small)
                .disabled(store.isConfigurationLocked)
        }
        .frame(maxWidth: .infinity, minHeight: 130)
    }

    private func applicationRow(_ application: RoutingApplication) -> some View {
        HStack(spacing: 10) {
            Image(nsImage: NSWorkspace.shared.icon(forFile: application.bundlePath))
                .resizable()
                .scaledToFit()
                .frame(width: 32, height: 32)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                Text(application.name)
                    .font(.callout.bold())
                    .lineLimit(1)
                Text(application.bundlePath)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .help(application.bundlePath)
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Picker("出口", selection: actionBinding(for: application.id)) {
                ForEach(RoutingAction.allCases) { action in
                    Label(action.title, systemImage: action.icon).tag(action)
                }
            }
            .labelsHidden()
            .pickerStyle(.menu)
            .frame(width: 165)
            .disabled(store.isConfigurationLocked)

            Button(role: .destructive) {
                store.removeApplication(id: application.id)
            } label: {
                Image(systemName: "trash")
            }
            .buttonStyle(.borderless)
            .help("移除规则")
            .accessibilityLabel("移除 \(application.name) 的分流规则")
            .disabled(store.isConfigurationLocked)
        }
        .padding(.vertical, 9)
    }

    private func actionBinding(for id: String) -> Binding<RoutingAction> {
        Binding(
            get: {
                store.config.applications.first(where: { $0.id == id })?.action ?? .systemDirect
            },
            set: { newValue in
                guard let index = store.config.applications.firstIndex(where: { $0.id == id }) else { return }
                store.config.applications[index].action = newValue
            })
    }

    private func readinessRow(title: String, value: String, icon: String, color: Color) -> some View {
        HStack(spacing: 10) {
            Image(systemName: icon)
                .foregroundStyle(color)
                .frame(width: 18)
                .accessibilityHidden(true)
            Text(title)
                .foregroundStyle(.secondary)
                .frame(width: 86, alignment: .leading)
            Text(value)
                .textSelection(.enabled)
            Spacer()
        }
        .font(.callout)
    }

    private func routingModuleDescription(_ module: RoutingInterfaceInfo) -> String {
        guard let ipv6 = module.ipv6, !ipv6.isEmpty else {
            return "\(module.name) · IPv4 \(module.ipv4) · IPv6 未分配"
        }
        return "\(module.name) · IPv4 \(module.ipv4) · IPv6 \(ipv6)"
    }

    private func noticeRow(_ message: String, color: Color, icon: String) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: icon)
                .foregroundStyle(color)
                .frame(width: 16)
                .accessibilityHidden(true)
            Text(message)
                .font(.caption)
                .foregroundStyle(.primary)
                .fixedSize(horizontal: false, vertical: true)
            Spacer()
        }
    }

    private var runtimeBadge: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(runtimeColor)
                .frame(width: 7, height: 7)
            Text(runtimeText)
                .font(.caption.bold())
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(Capsule().fill(runtimeColor.opacity(0.14)))
        .foregroundStyle(runtimeColor)
    }

    private var runtimeText: String {
        if store.isUninstalling { return "正在卸载服务" }
        if store.isSwitching {
            return store.pendingEnabled == false ? "正在停用" : "正在启用"
        }
        switch store.runtime.state {
        case "running": return "运行中"
        case "failed": return "启动失败"
        case "starting": return "正在启动"
        case "stopping": return "正在停止"
        default: return "未启用"
        }
    }

    private var runtimeColor: Color {
        if store.isUninstalling { return .orange }
        if store.isSwitching { return .orange }
        switch store.runtime.state {
        case "running": return .green
        case "failed": return .red
        case "starting", "stopping": return .orange
        default: return .secondary
        }
    }

    private var coreDescription: String {
        guard store.capabilities?.coreAvailable == true else { return "不可用" }
        if let version = store.capabilities?.coreVersion, !version.isEmpty {
            return "sing-box \(version)"
        }
        return "可用"
    }

    private func chooseApplications() {
        let panel = NSOpenPanel()
        panel.title = "选择需要分流的应用"
        panel.prompt = "添加"
        panel.directoryURL = URL(fileURLWithPath: "/Applications", isDirectory: true)
        panel.allowedContentTypes = [.applicationBundle]
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        guard panel.runModal() == .OK else { return }
        for url in panel.urls {
            store.addApplication(url: url)
        }
    }
}

private extension View {
    func routingPanel() -> some View {
        padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }
}

import SwiftUI
import UserNotifications

/// 调试与诊断：按功能分页组织（网络诊断 / AT 调试 / 通知调试），
/// 后续新增调试功能时新增一个 tab 及对应子视图即可，互不影响。
struct DiagnosticsView: View {
    enum DebugTab: String, CaseIterable, Identifiable {
        case network = "网络诊断"
        case at = "AT 调试"
        case notify = "通知调试"

        var id: String { rawValue }
    }

    @State private var tab: DebugTab = .network

    var body: some View {
        TabView(selection: $tab) {
            DiagnosticNetworkView()
                .tabItem { Label(DebugTab.network.rawValue, systemImage: "network") }
                .tag(DebugTab.network)
            DiagnosticATView()
                .tabItem { Label(DebugTab.at.rawValue, systemImage: "terminal") }
                .tag(DebugTab.at)
            DiagnosticNotifyView()
                .tabItem { Label(DebugTab.notify.rawValue, systemImage: "bell") }
                .tag(DebugTab.notify)
        }
        .navigationTitle("调试与诊断")
    }
}

// MARK: - 网络诊断

struct DiagnosticNetworkView: View {
    @EnvironmentObject private var backend: BackendProcess

    @State private var diagnostic: NetworkDiagnostic?
    @State private var diagnosticError: String?

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("网络诊断").font(.headline)
                if let diagnostic, let errors = diagnostic.errors, !errors.isEmpty {
                    Label("部分命令失败", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
                Spacer()
                Button {
                    loadDiagnostic()
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .controlSize(.small)
                .help("重新采集诊断信息")
            }
            .padding(16)
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    if let diagnostic {
                        detailRows(for: diagnostic)
                    } else if let diagnosticError {
                        Text(diagnosticError).font(.callout).foregroundStyle(.secondary)
                    } else {
                        HStack(spacing: 8) {
                            ProgressView().controlSize(.small)
                            Text("读取诊断信息…").foregroundStyle(.secondary)
                        }
                    }
                }
                .font(.callout)
                .padding(16)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .scrollIndicators(.visible)
        }
        .onAppear { loadDiagnostic() }
    }

    private func detailRows(for diagnostic: NetworkDiagnostic) -> some View {
        Group {
            detailRow("USB 网卡模式", diagnostic.usbnetMode ?? "-")
            if let usbcfg = diagnostic.usbcfg {
                detailRow("USB 配置", usbcfg)
            }
            if let present = diagnostic.usbNetworkPresent {
                detailRow("USB 网络接口", present ? "存在" : "不存在")
            }
            if let route = diagnostic.defaultRoute {
                detailRow("默认出口", "\(route.interface ?? "-") → \(route.gateway ?? "-")")
            }
            ForEach(diagnostic.macInterfaces ?? [], id: \.name) { iface in
                detailRow("网络接口", "\(iface.name ?? "-")（\(iface.kind ?? "-")，\(iface.status ?? "-")）\(iface.ipv4 ?? "")")
            }
            ForEach(diagnostic.pdpContexts ?? [], id: \.id) { ctx in
                detailRow("PDP 上下文 \(ctx.id ?? 0)", "\(ctx.pdn ?? "-") \(ctx.apn ?? "-")")
            }
            if let addresses = diagnostic.pdpAddresses, !addresses.isEmpty {
                detailRow("PDP 地址", addresses.joined(separator: "、"))
            }
            if let errors = diagnostic.errors, !errors.isEmpty {
                Divider()
                ForEach(errors.sorted(by: { $0.key < $1.key }), id: \.key) { key, value in
                    detailRow("\(key) 错误", value)
                }
            }
        }
    }

    private func detailRow(_ key: String, _ value: String) -> some View {
        HStack(alignment: .top) {
            Text(key).foregroundStyle(.secondary).frame(width: 118, alignment: .leading)
            Text(value).textSelection(.enabled)
            Spacer()
        }
    }

    private func loadDiagnostic() {
        guard case .running = backend.state else { return }
        Task {
            do {
                let diag: NetworkDiagnostic = try await APIClient().get("api/network")
                await MainActor.run {
                    diagnostic = diag
                    diagnosticError = nil
                }
            } catch {
                await MainActor.run { diagnosticError = error.localizedDescription }
            }
        }
    }
}

// MARK: - AT 调试

struct DiagnosticATView: View {
    @State private var command = ""
    @State private var history: [ATEntry] = []
    @State private var atBusy = false
    @State private var atError: String?

    private let quickCommands = ["AT", "AT+CSQ", "AT+COPS?", "AT+CPIN?", "AT+CNUM", "AT+QCFG=\"usbnet\""]

    var body: some View {
        VStack(spacing: 0) {
            quickBar
            Divider()
            outputArea
            Divider()
            inputBar
        }
    }

    private var quickBar: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                Text("快捷指令").font(.caption).foregroundStyle(.secondary)
                ForEach(quickCommands, id: \.self) { cmd in
                    Button(cmd) {
                        command = cmd
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }
                if let atError {
                    Text(atError).font(.caption).foregroundStyle(.red).lineLimit(1)
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
        }
    }

    private var outputArea: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    if history.isEmpty {
                        VStack(spacing: 8) {
                            Image(systemName: "terminal").font(.largeTitle).foregroundStyle(.tertiary)
                            Text("输入 AT 指令并发送，响应将显示在这里")
                                .font(.callout).foregroundStyle(.secondary)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.top, 40)
                    } else {
                        ForEach(history) { entry in
                            ATEntryRow(entry: entry)
                                .id(entry.id)
                        }
                    }
                }
                .padding(16)
            }
            .scrollIndicators(.visible)
            .onChange(of: history.count) { _ in
                if let last = history.last {
                    withAnimation {
                        proxy.scrollTo(last.id, anchor: .bottom)
                    }
                }
            }
        }
        .background(Color(nsColor: .textBackgroundColor))
    }

    private var inputBar: some View {
        HStack(spacing: 10) {
            TextField("例如：AT+CSQ", text: $command)
                .textFieldStyle(.roundedBorder)
                .font(.monospaced(.body)())
                .onSubmit { sendAT() }
            Button {
                sendAT()
            } label: {
                if atBusy {
                    ProgressView().controlSize(.small)
                } else {
                    Label("发送", systemImage: "paperplane")
                }
            }
            .buttonStyle(.borderedProminent)
            .disabled(atBusy || command.isEmpty)
        }
        .padding(16)
    }

    private func sendAT() {
        let cmd = command.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cmd.isEmpty else { return }
        atBusy = true
        let client = APIClient()
        Task {
            do {
                let result: ATResult = try await client.send(
                    "api/at", body: ATRequest(command: cmd))
                await MainActor.run {
                    history.append(ATEntry(command: cmd, response: result.response ?? ""))
                    atBusy = false
                    atError = nil
                }
            } catch {
                await MainActor.run {
                    history.append(ATEntry(command: cmd, response: "错误：\(error.localizedDescription)", isError: true))
                    atBusy = false
                    atError = error.localizedDescription
                }
            }
        }
        command = ""
    }
}

// MARK: - 通知调试

struct DiagnosticNotifyView: View {
    @EnvironmentObject private var smsStore: SMSStore
    @EnvironmentObject private var store: DashboardStore

    @State private var notifyAuth: String?
    @State private var notifyFeedback: String?
    /// 已授权但通知样式被系统设为"无"（静默，不弹横幅）
    @State private var notifyBannerOff = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("通知调试").font(.headline)

            HStack(spacing: 8) {
                Text("通知权限")
                if let notifyAuth {
                    Text(notifyAuth)
                        .font(.callout)
                        .foregroundStyle(notifyAuth == "已开启" ? Color.green : Color.orange)
                } else {
                    ProgressView().controlSize(.small)
                }
                if notifyBannerOff {
                    Button("打开通知设置") {
                        openNotifySettings()
                    }
                    .controlSize(.small)
                    .help("在系统设置中将通知样式改为横幅，通知才会弹出")
                }
            }

            if let notifyFeedback {
                Text(notifyFeedback)
                    .font(.caption)
                    .foregroundStyle(notifyFeedback.contains("成功") || notifyFeedback.contains("已发送") ? Color.secondary : Color.red)
            }

            HStack(spacing: 8) {
                Button {
                    simulateNotify()
                } label: {
                    Label("模拟短信通知", systemImage: "bell.badge")
                }
                .buttonStyle(.bordered)
                .help("直接发送一条测试通知，验证新短信系统通知是否正常工作")

                Button {
                    simulateCallNotify()
                } label: {
                    Label("模拟来电通知", systemImage: "phone.fill")
                }
                .buttonStyle(.bordered)
                .help("直接弹出一张自定义来电卡片，验证来电提醒是否正常工作")
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .onAppear { loadNotifyAuth() }
    }

    private func loadNotifyAuth() {
        Task {
            let settings = await UNUserNotificationCenter.current().notificationSettings()
            let text: String
            switch settings.authorizationStatus {
            case .authorized, .provisional, .ephemeral: text = "已开启"
            case .notDetermined: text = "未授权"
            case .denied: text = "已拒绝"
            @unknown default: text = "未知"
            }
            await MainActor.run {
                notifyAuth = text
                notifyBannerOff = settings.authorizationStatus == .authorized && settings.alertSetting == .disabled
            }
        }
    }

    private func openNotifySettings() {
        // macOS 13+：打开系统设置的通知面板
        if let url = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension") {
            NSWorkspace.shared.open(url)
        }
    }

    private func simulateNotify() {
        Task {
            notifyFeedback = "正在发送…"
            notifyFeedback = await smsStore.simulateNotification()
            loadNotifyAuth()
        }
    }

    private func simulateCallNotify() {
        notifyFeedback = "已弹出模拟来电卡片（30 秒后自动收起）"
        IncomingCallCard.shared.show(store: store, preview: true)
    }
}

// MARK: - AT 记录

struct ATEntry: Identifiable {
    let id = UUID()
    let command: String
    let response: String
    var isError: Bool = false
}

struct ATEntryRow: View {
    let entry: ATEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(entry.command)
                .font(.monospaced(.body)())
                .textSelection(.enabled)
            Text(entry.response.isEmpty ? "(无响应)" : entry.response)
                .font(.monospaced(.body)())
                .foregroundStyle(entry.isError ? Color.red : Color(nsColor: .secondaryLabelColor))
                .textSelection(.enabled)
        }
        .padding(.vertical, 6)
        .padding(.horizontal, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: 8)
                .fill(entry.isError ? Color.red.opacity(0.08) : Color.accentColor.opacity(0.05)))
        .padding(.vertical, 3)
    }
}

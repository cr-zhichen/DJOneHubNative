import SwiftUI

/// 调试与诊断：网络诊断信息 + AT 指令调试
struct DiagnosticsView: View {
    @EnvironmentObject private var backend: BackendProcess

    @State private var diagnostic: NetworkDiagnostic?
    @State private var diagnosticError: String?
    @State private var showDiagnostic = false

    @State private var command = ""
    @State private var history: [ATEntry] = []
    @State private var atBusy = false
    @State private var atError: String?

    private let quickCommands = ["AT", "AT+CSQ", "AT+COPS?", "AT+CPIN?", "AT+CNUM", "AT+QCFG=\"usbnet\""]

    var body: some View {
        VStack(spacing: 0) {
            diagnosticSection
            Divider()
            atHeader
            Divider()
            outputArea
            Divider()
            inputBar
        }
        .navigationTitle("调试与诊断")
        .onAppear { loadDiagnostic() }
        .sheet(isPresented: $showDiagnostic) {
            DiagnosticDetailView(diagnostic: diagnostic, error: diagnosticError)
        }
    }

    // MARK: - 网络诊断

    private var diagnosticSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Text("网络诊断").font(.headline)
                if let diagnostic, let errors = diagnostic.errors, !errors.isEmpty {
                    Text("部分命令失败").font(.caption).foregroundStyle(.orange)
                }
                Spacer()
                Button {
                    loadDiagnostic()
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .controlSize(.small)
                .help("重新采集诊断信息")
                Button("查看详情") {
                    showDiagnostic = true
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
            if let diagnostic {
                Text(summaryText)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            } else if let diagnosticError {
                Text(diagnosticError).font(.callout).foregroundStyle(.secondary)
            } else {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("读取诊断信息…").foregroundStyle(.secondary)
                }
            }
        }
        .padding(14)
    }

    private var summaryText: String {
        guard let diagnostic else { return "-" }
        var parts: [String] = []
        if let mode = diagnostic.usbnetMode {
            parts.append("网卡模式 \(mode)")
        }
        if let present = diagnostic.usbNetworkPresent {
            parts.append(present ? "USB 网络接口存在" : "无 USB 网络接口")
        }
        if let route = diagnostic.defaultRoute, let iface = route.interface {
            parts.append("默认出口 \(iface)")
        }
        if let count = diagnostic.macInterfaces?.count {
            parts.append("网络接口 \(count) 个")
        }
        if let count = diagnostic.pdpContexts?.count {
            parts.append("PDP 上下文 \(count) 个")
        }
        return parts.isEmpty ? "-" : parts.joined(separator: " · ")
    }

    // MARK: - AT 调试

    private var atHeader: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                Text("AT 调试").font(.headline)
                ForEach(quickCommands, id: \.self) { cmd in
                    Button(cmd) {
                        command = cmd
                    }
                    .controlSize(.small)
                }
                Spacer()
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

    // MARK: - 数据

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

/// 网络诊断详情弹窗（内容可竖向滚动）
struct DiagnosticDetailView: View {
    @Environment(\.dismiss) private var dismiss
    let diagnostic: NetworkDiagnostic?
    let error: String?

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("网络诊断").font(.headline)
                Spacer()
                Button("完成") { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }
            .padding(16)
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    if let diagnostic {
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
                    } else if let error {
                        Text(error).font(.callout).foregroundStyle(.secondary)
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
        .frame(minWidth: 540, minHeight: 420, maxHeight: 620)
    }

    private func detailRow(_ key: String, _ value: String) -> some View {
        HStack(alignment: .top) {
            Text(key).foregroundStyle(.secondary).frame(width: 118, alignment: .leading)
            Text(value).textSelection(.enabled)
            Spacer()
        }
    }
}

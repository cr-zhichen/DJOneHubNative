import AppKit
import SwiftUI
import UniformTypeIdentifiers

struct RuntimeDiagnosticsView: View {
    @EnvironmentObject private var backend: BackendProcess
    @EnvironmentObject private var store: DashboardStore
    @StateObject private var eventLog = StructuredDiagnosticLogStore.live()

    @State private var generatedAt = Date()
    @State private var exportError: String?
    @State private var copied = false
    @State private var showingClearLogConfirmation = false

    private var snapshot: RuntimeDiagnosticsSnapshot {
        RuntimeDiagnosticsSnapshot.build(
            serviceRunning: backend.state == .running,
            serviceFailed: backendFailure != nil,
            statusStale: store.statusStale,
            moduleConnected: store.health?.usbDevice != nil || store.status?.usbDevice != nil,
            callModeState: store.callModeStatus?.state,
            callModeSummary: store.callModeStatus?.summary,
            callStatus: store.callStatus,
            verification: store.voiceVerification,
            agentAudio: store.agentAudioStatus,
            agentStatusReadFailed: store.agentAudioStatusError != nil,
            generatedAt: generatedAt)
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    overallSummary
                    diagnosticRows
                    structuredEvents
                    privacyNotice
                    if let storageError = eventLog.storageError {
                        Label(storageError, systemImage: "exclamationmark.triangle.fill")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }
                    if let exportError {
                        Label(exportError, systemImage: "exclamationmark.triangle.fill")
                            .font(.caption)
                            .foregroundStyle(.red)
                    }
                }
                .padding(16)
                .frame(maxWidth: 880)
                .frame(maxWidth: .infinity, alignment: .top)
            }
            .scrollIndicators(.visible)
        }
        .onAppear {
            eventLog.record(snapshot: snapshot)
            refreshAll()
        }
        .onChange(of: snapshot) { updated in
            eventLog.record(snapshot: updated)
        }
        .confirmationDialog(
            "清空结构化诊断事件？",
            isPresented: $showingClearLogConfirmation,
            titleVisibility: .visible
        ) {
            Button("清空", role: .destructive) { eventLog.clear() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("只会清除本机保存的隐私安全状态事件，不会修改模块、通话或 AI CLI 权限。")
        }
    }

    private var header: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text("运行诊断").font(.headline)
                Text("分层检查服务、模块、通话、Agent PCM 与 ASR，不用单个“可用”状态代替真实链路。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                copyReport()
            } label: {
                Label(copied ? "已复制" : "复制摘要", systemImage: copied ? "checkmark" : "doc.on.doc")
            }
            .controlSize(.small)
            Button {
                exportReport()
            } label: {
                Label("导出支持报告…", systemImage: "square.and.arrow.up")
            }
            .controlSize(.small)
            Button {
                refreshAll()
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .controlSize(.small)
            .help("刷新全部运行状态")
            .accessibilityLabel("刷新全部运行状态")
        }
        .padding(16)
    }

    private var overallSummary: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: levelIcon(snapshot.overallLevel))
                .font(.title2)
                .foregroundStyle(levelColor(snapshot.overallLevel))
                .frame(width: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 3) {
                Text(overallTitle)
                    .font(.headline)
                Text(overallDetail)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Text(snapshot.overallLevel.displayName)
                .font(.caption.weight(.semibold))
                .foregroundStyle(levelColor(snapshot.overallLevel))
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(levelColor(snapshot.overallLevel).opacity(0.1))
                .clipShape(Capsule())
        }
        .padding(14)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).stroke(Color(nsColor: .separatorColor)))
    }

    private var diagnosticRows: some View {
        VStack(spacing: 0) {
            ForEach(Array(snapshot.items.enumerated()), id: \.element.id) { index, item in
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: levelIcon(item.level))
                        .foregroundStyle(levelColor(item.level))
                        .frame(width: 20, height: 20)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Text(item.title)
                                .font(.callout.weight(.medium))
                            Spacer()
                            Text(item.summary)
                                .font(.callout)
                                .foregroundStyle(levelColor(item.level))
                        }
                        Text(item.detail)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .accessibilityElement(children: .combine)
                if index < snapshot.items.count - 1 {
                    Divider().padding(.leading, 44)
                }
            }
        }
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).stroke(Color(nsColor: .separatorColor)))
    }

    private var structuredEvents: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("结构化事件")
                        .font(.callout.weight(.semibold))
                    Text("仅记录组件、状态级别与时间；最多保留 200 条。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Text("\(eventLog.events.count) 条")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                Button("清空…", role: .destructive) {
                    showingClearLogConfirmation = true
                }
                .controlSize(.small)
                .disabled(eventLog.events.isEmpty)
            }
            .padding(14)

            if eventLog.events.isEmpty {
                Divider()
                Text("暂无结构化事件")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(14)
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                ForEach(Array(eventLog.events.suffix(20).reversed())) { event in
                    Divider().padding(.leading, 14)
                    HStack(alignment: .firstTextBaseline, spacing: 10) {
                        Text(event.timestamp.formatted(date: .omitted, time: .standard))
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.secondary)
                            .frame(width: 78, alignment: .leading)
                        Text(event.component.displayName)
                            .font(.caption.weight(.medium))
                        Spacer()
                        Text(event.level.displayName)
                            .font(.caption.weight(.medium))
                            .foregroundStyle(eventColor(event.level))
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 8)
                    .accessibilityElement(children: .combine)
                }
            }
        }
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).stroke(Color(nsColor: .separatorColor)))
    }

    private var privacyNotice: some View {
        Label(
            "支持报告只包含固定状态和结构化事件，不包含原始日志、电话号码、IMEI、ICCID、IMSI、会话 ID、转写内容、验证码或 Socket 路径。",
            systemImage: "lock.shield")
            .font(.caption)
            .foregroundStyle(.secondary)
    }

    private var backendFailure: String? {
        if case .failed(let reason) = backend.state { return reason }
        return nil
    }

    private var overallTitle: String {
        switch snapshot.overallLevel {
        case .healthy: return "关键运行链路正常"
        case .warning: return "存在需要关注的运行状态"
        case .critical: return "关键运行链路不可用"
        case .neutral: return "运行状态待确认"
        }
    }

    private var overallDetail: String {
        let date = generatedAt.formatted(date: .omitted, time: .standard)
        switch snapshot.overallLevel {
        case .healthy: return "基础能力已就绪；空闲组件不会被误报为故障。更新于 \(date)"
        case .warning: return "请按下方分层状态定位未就绪环节。更新于 \(date)"
        case .critical: return "先恢复红色故障项，再执行通话或 Agent 操作。更新于 \(date)"
        case .neutral: return "等待状态读取完成。更新于 \(date)"
        }
    }

    private func refreshAll() {
        generatedAt = Date()
        exportError = nil
        copied = false
        store.refresh()
        store.pollCallModeStatus()
        store.pollCallStatus()
        store.pollVoiceVerification()
        store.pollAgentAudioStatus()
    }

    private func copyReport() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(snapshot.sanitizedReport, forType: .string)
        copied = true
        exportError = nil
    }

    private func exportReport() {
        let panel = NSSavePanel()
        panel.title = "导出隐私安全支持报告"
        panel.nameFieldStringValue = "Flame-DJ-Hub-Support-Report.txt"
        panel.allowedContentTypes = [.plainText]
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            let report = PrivacySafeSupportReport.render(snapshot: snapshot, events: eventLog.events)
            try report.write(to: url, atomically: true, encoding: .utf8)
            exportError = nil
        } catch {
            exportError = "导出失败：\(error.localizedDescription)"
        }
    }

    private func levelIcon(_ level: RuntimeDiagnosticLevel) -> String {
        switch level {
        case .healthy: return "checkmark.circle.fill"
        case .warning: return "exclamationmark.triangle.fill"
        case .critical: return "xmark.octagon.fill"
        case .neutral: return "circle.dotted"
        }
    }

    private func levelColor(_ level: RuntimeDiagnosticLevel) -> Color {
        switch level {
        case .healthy: return .green
        case .warning: return .orange
        case .critical: return .red
        case .neutral: return .secondary
        }
    }

    private func eventColor(_ level: DiagnosticEventLevel) -> Color {
        switch level {
        case .healthy: return .green
        case .warning: return .orange
        case .critical: return .red
        case .neutral: return .secondary
        }
    }
}

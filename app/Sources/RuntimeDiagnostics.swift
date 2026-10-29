import Foundation

enum RuntimeDiagnosticLevel: String, Equatable {
    case healthy
    case warning
    case critical
    case neutral

    var displayName: String {
        switch self {
        case .healthy: return "正常"
        case .warning: return "需关注"
        case .critical: return "故障"
        case .neutral: return "待机"
        }
    }
}

struct RuntimeDiagnosticItem: Identifiable, Equatable {
    let id: String
    let title: String
    let summary: String
    let detail: String
    let level: RuntimeDiagnosticLevel
}

struct RuntimeDiagnosticsSnapshot: Equatable {
    let generatedAt: Date
    let overallLevel: RuntimeDiagnosticLevel
    let items: [RuntimeDiagnosticItem]

    func item(id: String) -> RuntimeDiagnosticItem? {
        items.first { $0.id == id }
    }

    var sanitizedReport: String {
        let formatter = ISO8601DateFormatter()
        var lines = [
            "Flame DJ Hub 运行诊断",
            "生成时间：\(formatter.string(from: generatedAt))",
            "总体状态：\(overallLevel.displayName)",
        ]
        lines.append(contentsOf: items.compactMap { Self.safeReportLine(for: $0) })
        lines.append("隐私说明：报告仅包含固定组件名称、状态级别与经过格式校验的非敏感计数，不包含自由文本或业务数据。")
        return lines.joined(separator: "\n")
    }

    private static func safeReportLine(for item: RuntimeDiagnosticItem) -> String? {
        guard let title = safeReportTitle(for: item.id) else { return nil }
        var line = "[\(item.level.displayName)] \(title)"
        guard item.id == "pcm-capture" else { return line }

        let parts = item.detail.split(separator: " ", omittingEmptySubsequences: true)
        guard parts.count == 5,
              parts[1] == "帧",
              parts[2] == "·",
              parts[3] == "非零",
              parts[0].allSatisfy(\.isNumber),
              parts[4].allSatisfy(\.isNumber) else { return line }
        line += "：\(parts[0]) 帧 · 非零 \(parts[4])"
        return line
    }

    private static func safeReportTitle(for id: String) -> String? {
        switch id {
        case "service": return "后台服务"
        case "module": return "蜂窝模块"
        case "call-mode": return "通话模式"
        case "call": return "蜂窝通话"
        case "verification": return "一次性接听授权"
        case "agent-transport": return "Agent PCM 传输"
        case "agent-client": return "Agent 客户端"
        case "module-io": return "模块音频 IO"
        case "pcm-capture": return "PCM 采集"
        case "asr": return "本地 ASR"
        default: return nil
        }
    }

    static func build(
        serviceRunning: Bool,
        serviceFailed: Bool,
        statusStale: Bool,
        moduleConnected: Bool,
        callModeState: String?,
        callModeSummary: String?,
        callStatus: CallStatus,
        verification: VoiceVerificationAudioStatus,
        agentAudio: AgentAudioStatus?,
        agentStatusReadFailed: Bool,
        generatedAt: Date = Date()
    ) -> RuntimeDiagnosticsSnapshot {
        let expectsAgentChain = VoiceVerificationAudioPolicy.reservesAgentAudio(
            callSessionID: callStatus.sessionID,
            callActive: callStatus.isActive,
            verification: verification)

        var rows: [RuntimeDiagnosticItem] = []
        rows.append(serviceItem(running: serviceRunning, failed: serviceFailed, stale: statusStale))
        rows.append(moduleItem(connected: moduleConnected, serviceRunning: serviceRunning))
        rows.append(callModeItem(state: callModeState, summary: callModeSummary))
        rows.append(callItem(status: callStatus))
        rows.append(verificationItem(status: verification))
        rows.append(contentsOf: agentItems(
            status: agentAudio,
            readFailed: agentStatusReadFailed,
            expectsAgentChain: expectsAgentChain))

        let overall: RuntimeDiagnosticLevel
        if rows.contains(where: { $0.level == .critical }) {
            overall = .critical
        } else if rows.contains(where: { $0.level == .warning }) {
            overall = .warning
        } else {
            overall = .healthy
        }
        return RuntimeDiagnosticsSnapshot(generatedAt: generatedAt, overallLevel: overall, items: rows)
    }

    private static func serviceItem(running: Bool, failed: Bool, stale: Bool) -> RuntimeDiagnosticItem {
        if failed {
            return .init(id: "service", title: "后台服务", summary: "启动失败", detail: "请重新启动服务", level: .critical)
        }
        if !running {
            return .init(id: "service", title: "后台服务", summary: "未运行", detail: "诊断数据不可用", level: .critical)
        }
        if stale {
            return .init(id: "service", title: "后台服务", summary: "运行中", detail: "模块状态数据已过期", level: .warning)
        }
        return .init(id: "service", title: "后台服务", summary: "运行中", detail: "状态读取正常", level: .healthy)
    }

    private static func moduleItem(connected: Bool, serviceRunning: Bool) -> RuntimeDiagnosticItem {
        guard serviceRunning else {
            return .init(id: "module", title: "蜂窝模块", summary: "未知", detail: "等待后台服务", level: .neutral)
        }
        return connected
            ? .init(id: "module", title: "蜂窝模块", summary: "已连接", detail: "USB 设备已识别", level: .healthy)
            : .init(id: "module", title: "蜂窝模块", summary: "未连接", detail: "请检查 USB 与模块供电", level: .critical)
    }

    private static func callModeItem(state: String?, summary _: String?) -> RuntimeDiagnosticItem {
        switch state {
        case "ready":
            return .init(id: "call-mode", title: "通话模式", summary: "已就绪", detail: "模块接口与运行时可用", level: .healthy)
        case "authorizing_adb", "enabling_usb", "enabling_voice", "restarting", "verifying_usb", "downloading", "preparing", "restoring_usb", "restarting_restore":
            return .init(id: "call-mode", title: "通话模式", summary: "准备中", detail: "等待当前操作完成", level: .warning)
        case "failed", "disconnected", "unsupported":
            return .init(id: "call-mode", title: "通话模式", summary: "不可用", detail: "需要重新检测或修复", level: .critical)
        case nil:
            return .init(id: "call-mode", title: "通话模式", summary: "正在读取", detail: "尚未取得状态", level: .warning)
        default:
            return .init(id: "call-mode", title: "通话模式", summary: "需要准备", detail: "按页面提示完成准备", level: .warning)
        }
    }

    private static func callItem(status: CallStatus) -> RuntimeDiagnosticItem {
        switch status.state {
        case "idle":
            return .init(id: "call", title: "蜂窝通话", summary: "空闲", detail: "当前没有通话", level: .neutral)
        case "incoming":
            return .init(id: "call", title: "蜂窝通话", summary: "来电中", detail: "等待接听或结束", level: .healthy)
        case "active":
            return .init(id: "call", title: "蜂窝通话", summary: "通话中", detail: "蜂窝通话已接通", level: .healthy)
        default:
            return .init(id: "call", title: "蜂窝通话", summary: "状态未知", detail: "不会执行新的呼叫操作", level: .warning)
        }
    }

    private static func verificationItem(status: VoiceVerificationAudioStatus) -> RuntimeDiagnosticItem {
        if let error = status.lastError, !error.isEmpty {
            return .init(id: "verification", title: "一次性接听授权", summary: "执行失败", detail: "请重新开启授权窗口", level: .warning)
        }
        if status.answered && status.consumed {
            return .init(
                id: "verification",
                title: "一次性接听授权",
                summary: status.audioAuthorized ? "已消费并授权音频" : "已消费",
                detail: "不会再次自动接听",
                level: status.audioAuthorized ? .healthy : .neutral)
        }
        if status.active {
            return .init(id: "verification", title: "一次性接听授权", summary: "等待指定来电", detail: "限时且单次消费", level: .neutral)
        }
        return .init(id: "verification", title: "一次性接听授权", summary: "未开启", detail: "不会自动接听", level: .neutral)
    }

    private static func agentItems(
        status: AgentAudioStatus?,
        readFailed: Bool,
        expectsAgentChain: Bool
    ) -> [RuntimeDiagnosticItem] {
        guard let status else {
            let level: RuntimeDiagnosticLevel = readFailed ? .warning : .neutral
            let summary = readFailed ? "读取失败" : "正在读取"
            return [
                .init(id: "agent-transport", title: "Agent PCM 传输", summary: summary, detail: "尚无可用状态", level: level),
                .init(id: "agent-client", title: "Agent 客户端", summary: "未知", detail: "尚无可用状态", level: level),
                .init(id: "module-io", title: "模块音频 IO", summary: "未知", detail: "尚无可用状态", level: level),
                .init(id: "pcm-capture", title: "PCM 采集", summary: "未知", detail: "尚无可用状态", level: level),
                .init(id: "asr", title: "本地 ASR", summary: "未知", detail: "尚无可用状态", level: level),
            ]
        }

        let transport: RuntimeDiagnosticItem
        if !status.compiled {
            transport = .init(
                id: "agent-transport",
                title: "Agent PCM 传输",
                summary: "未编译",
                detail: expectsAgentChain ? "当前授权通话需要 Agent PCM" : "当前构建未包含可选 Agent PCM 能力",
                level: expectsAgentChain ? .critical : .neutral)
        } else if status.transportReady && status.pcmTransportAvailable {
            transport = .init(id: "agent-transport", title: "Agent PCM 传输", summary: "已就绪", detail: "本地传输可用", level: .healthy)
        } else {
            transport = .init(id: "agent-transport", title: "Agent PCM 传输", summary: "未就绪", detail: "检查本地传输状态", level: .warning)
        }

        let client = RuntimeDiagnosticItem(
            id: "agent-client",
            title: "Agent 客户端",
            summary: status.clientConnected ? "已连接" : "未连接",
            detail: status.clientConnected ? "客户端握手已完成" : (expectsAgentChain ? "授权通话中应建立连接" : "空闲时允许未连接"),
            level: status.clientConnected ? .healthy : (expectsAgentChain ? .warning : .neutral))

        let moduleIO = RuntimeDiagnosticItem(
            id: "module-io",
            title: "模块音频 IO",
            summary: status.moduleIOReady ? "已就绪" : "未就绪",
            detail: status.moduleIOReady ? "AC 与 AS 均有真实回调" : (expectsAgentChain ? "授权通话中缺少真实回调" : "空闲时不会占用模块音频"),
            level: status.moduleIOReady ? .healthy : (expectsAgentChain ? .warning : .neutral))

        let capture: RuntimeDiagnosticItem
        if status.captureActive && status.framesReceived > 0 && status.nonzeroFrames > 0 {
            capture = .init(
                id: "pcm-capture", title: "PCM 采集", summary: "正在接收",
                detail: "\(status.framesReceived) 帧 · 非零 \(status.nonzeroFrames)", level: .healthy)
        } else if expectsAgentChain {
            let detail: String
            if !status.captureActive {
                detail = "授权通话中采集未启动"
            } else if status.framesReceived == 0 {
                detail = "采集已启动但尚无帧"
            } else {
                detail = "已接收 \(status.framesReceived) 帧，但没有非零音频"
            }
            capture = .init(id: "pcm-capture", title: "PCM 采集", summary: "未形成有效音频", detail: detail, level: .warning)
        } else {
            capture = .init(id: "pcm-capture", title: "PCM 采集", summary: "待机", detail: "通话授权后启动", level: .neutral)
        }

        let asr: RuntimeDiagnosticItem
        if let error = status.asrError, !error.isEmpty {
            asr = .init(id: "asr", title: "本地 ASR", summary: "运行失败", detail: "请检查语音识别权限与资源", level: .warning)
        } else if status.effectiveASRAvailable {
            asr = .init(id: "asr", title: "本地 ASR", summary: "运行中", detail: "识别器已启动", level: .healthy)
        } else if expectsAgentChain {
            asr = .init(id: "asr", title: "本地 ASR", summary: "未启动", detail: "授权通话中应启动识别器", level: .warning)
        } else if status.asrCompiled {
            asr = .init(id: "asr", title: "本地 ASR", summary: "待机", detail: "已编译，通话时启动", level: .neutral)
        } else {
            asr = .init(id: "asr", title: "本地 ASR", summary: "不可用", detail: "当前构建未包含识别能力", level: .neutral)
        }

        return [transport, client, moduleIO, capture, asr]
    }
}

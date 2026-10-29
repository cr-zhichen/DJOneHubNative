import Foundation

enum DiagnosticEventLevel: String, Codable, Equatable {
    case healthy
    case warning
    case critical
    case neutral

    init(_ level: RuntimeDiagnosticLevel) {
        switch level {
        case .healthy: self = .healthy
        case .warning: self = .warning
        case .critical: self = .critical
        case .neutral: self = .neutral
        }
    }

    var displayName: String {
        switch self {
        case .healthy: return "正常"
        case .warning: return "需关注"
        case .critical: return "故障"
        case .neutral: return "待机"
        }
    }
}

enum DiagnosticEventComponent: String, Codable, CaseIterable, Equatable {
    case service
    case module
    case callMode = "call_mode"
    case call
    case verification
    case agentTransport = "agent_transport"
    case agentClient = "agent_client"
    case moduleIO = "module_io"
    case pcmCapture = "pcm_capture"
    case asr

    init?(diagnosticID: String) {
        switch diagnosticID {
        case "service": self = .service
        case "module": self = .module
        case "call-mode": self = .callMode
        case "call": self = .call
        case "verification": self = .verification
        case "agent-transport": self = .agentTransport
        case "agent-client": self = .agentClient
        case "module-io": self = .moduleIO
        case "pcm-capture": self = .pcmCapture
        case "asr": self = .asr
        default: return nil
        }
    }

    var displayName: String {
        switch self {
        case .service: return "后台服务"
        case .module: return "蜂窝模块"
        case .callMode: return "通话模式"
        case .call: return "蜂窝通话"
        case .verification: return "一次性接听授权"
        case .agentTransport: return "Agent PCM 传输"
        case .agentClient: return "Agent 客户端"
        case .moduleIO: return "模块音频 IO"
        case .pcmCapture: return "PCM 采集"
        case .asr: return "本地 ASR"
        }
    }
}

enum DiagnosticEventCode: String, Codable, Equatable {
    case baseline
    case stateChanged = "state_changed"
}

struct StructuredDiagnosticEvent: Identifiable, Codable, Equatable {
    let id: UUID
    let timestamp: Date
    let component: DiagnosticEventComponent
    let code: DiagnosticEventCode
    let level: DiagnosticEventLevel
    let previousLevel: DiagnosticEventLevel?

    init(
        id: UUID = UUID(),
        timestamp: Date,
        component: DiagnosticEventComponent,
        code: DiagnosticEventCode,
        level: DiagnosticEventLevel,
        previousLevel: DiagnosticEventLevel?
    ) {
        self.id = id
        self.timestamp = timestamp
        self.component = component
        self.code = code
        self.level = level
        self.previousLevel = previousLevel
    }

    enum CodingKeys: String, CodingKey {
        case id, timestamp, component, code, level
        case previousLevel = "previous_level"
    }

    var ndjsonLine: String {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        guard let data = try? encoder.encode(self),
              let line = String(data: data, encoding: .utf8) else { return "" }
        return line
    }
}

struct DiagnosticEventBuffer {
    let maxCount: Int
    private(set) var events: [StructuredDiagnosticEvent]
    private var lastLevels: [DiagnosticEventComponent: DiagnosticEventLevel]

    init(maxCount: Int = 200, events: [StructuredDiagnosticEvent] = []) {
        self.maxCount = max(1, maxCount)
        self.events = Array(events.suffix(max(1, maxCount)))
        self.lastLevels = [:]
        for event in self.events {
            lastLevels[event.component] = event.level
        }
    }

    mutating func record(
        snapshot: RuntimeDiagnosticsSnapshot,
        observedAt: Date? = nil
    ) -> [StructuredDiagnosticEvent] {
        let timestamp = observedAt ?? snapshot.generatedAt
        var created: [StructuredDiagnosticEvent] = []
        for item in snapshot.items {
            guard let component = DiagnosticEventComponent(diagnosticID: item.id) else { continue }
            let level = DiagnosticEventLevel(item.level)
            let previous = lastLevels[component]
            guard previous != level else { continue }
            let event = StructuredDiagnosticEvent(
                timestamp: timestamp,
                component: component,
                code: previous == nil ? .baseline : .stateChanged,
                level: level,
                previousLevel: previous)
            lastLevels[component] = level
            events.append(event)
            created.append(event)
        }
        if events.count > maxCount {
            events.removeFirst(events.count - maxCount)
        }
        return created
    }
}

enum DiagnosticEventArchive {
    static let maxArchiveBytes = 1_048_576
    static let maxLineBytes = 4_096

    static func encode(_ events: [StructuredDiagnosticEvent]) -> Data {
        Data(events.map(\.ndjsonLine).filter { !$0.isEmpty }.joined(separator: "\n").utf8)
    }

    static func decode(
        _ data: Data,
        maxCount: Int,
        now: Date = Date()
    ) -> [StructuredDiagnosticEvent]? {
        guard data.count <= maxArchiveBytes,
              let text = String(data: data, encoding: .utf8) else { return nil }
        if text.isEmpty { return [] }

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let requiredKeys: Set<String> = ["id", "timestamp", "component", "code", "level"]
        let allowedKeys = requiredKeys.union(["previous_level"])
        let earliest = Date(timeIntervalSince1970: 1_577_836_800)
        let latest = now.addingTimeInterval(300)
        var seenIDs = Set<UUID>()
        var lastTimestamp: Date?
        var lastLevels: [DiagnosticEventComponent: DiagnosticEventLevel] = [:]
        var events: [StructuredDiagnosticEvent] = []

        for line in text.split(whereSeparator: \.isNewline) {
            guard line.utf8.count <= maxLineBytes else { return nil }
            let lineData = Data(line.utf8)
            guard let object = try? JSONSerialization.jsonObject(with: lineData),
                  let dictionary = object as? [String: Any] else { return nil }
            let keys = Set(dictionary.keys)
            guard requiredKeys.isSubset(of: keys), keys.isSubset(of: allowedKeys),
                  let event = try? decoder.decode(StructuredDiagnosticEvent.self, from: lineData),
                  seenIDs.insert(event.id).inserted,
                  event.timestamp >= earliest,
                  event.timestamp <= latest else { return nil }
            if let lastTimestamp, event.timestamp < lastTimestamp { return nil }

            switch event.code {
            case .baseline:
                guard event.previousLevel == nil, lastLevels[event.component] == nil else { return nil }
            case .stateChanged:
                guard let previous = event.previousLevel, previous != event.level else { return nil }
                if let known = lastLevels[event.component], known != previous { return nil }
            }

            lastTimestamp = event.timestamp
            lastLevels[event.component] = event.level
            events.append(event)
        }
        return Array(events.suffix(max(1, maxCount)))
    }
}

enum PrivacySafeSupportReport {
    static func render(
        snapshot: RuntimeDiagnosticsSnapshot,
        events: [StructuredDiagnosticEvent]
    ) -> String {
        let formatter = ISO8601DateFormatter()
        var lines = [snapshot.sanitizedReport, "", "结构化事件（最近 \(events.count) 条）"]
        if events.isEmpty {
            lines.append("暂无结构化事件。")
        } else {
            lines.append(contentsOf: events.map { event in
                let transition: String
                if let previous = event.previousLevel {
                    transition = "\(previous.displayName) → \(event.level.displayName)"
                } else {
                    transition = event.level.displayName
                }
                return "\(formatter.string(from: event.timestamp)) [\(event.component.displayName)] \(transition)"
            })
        }
        lines.append("")
        lines.append("事件隐私说明：结构化事件仅记录固定组件、状态级别和时间，不记录自由文本或业务数据。")
        return lines.joined(separator: "\n")
    }
}

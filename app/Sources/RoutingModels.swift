import Foundation

enum RoutingMode: String, Codable, CaseIterable, Identifiable {
    case independent
    case clash

    var id: String { rawValue }

    var title: String {
        switch self {
        case .independent: return "独立分流"
        case .clash: return "Clash 代管"
        }
    }
}

enum RoutingAction: String, Codable, CaseIterable, Identifiable {
    case moduleDirect = "module_direct"
    case systemDirect = "system_direct"
    case systemSOCKS = "system_socks"

    var id: String { rawValue }

    static let defaultExitCases: [RoutingAction] = [.systemDirect, .moduleDirect, .systemSOCKS]

    var title: String {
        switch self {
        case .moduleDirect: return "4G 直连"
        case .systemDirect: return "系统直连"
        case .systemSOCKS: return "系统侧 SOCKS"
        }
    }

    var icon: String {
        switch self {
        case .moduleDirect: return "antenna.radiowaves.left.and.right"
        case .systemDirect: return "network"
        case .systemSOCKS: return "arrow.triangle.branch"
        }
    }
}

struct RoutingApplication: Codable, Identifiable, Equatable {
    var id: String
    var name: String
    var bundleID: String?
    var bundlePath: String
    var executablePath: String?
    var action: RoutingAction

    enum CodingKeys: String, CodingKey {
        case id, name, action
        case bundleID = "bundle_id"
        case bundlePath = "bundle_path"
        case executablePath = "executable_path"
    }
}

struct RoutingSOCKSConfig: Codable, Equatable {
    var server: String
    var port: Int
    var username: String
    var password: String

    init(server: String = "127.0.0.1", port: Int = 7891, username: String = "", password: String = "") {
        self.server = server
        self.port = port
        self.username = username
        self.password = password
    }
}

struct RoutingSOCKSCheckResult: Decodable, Equatable {
    let available: Bool
    let address: String?
    let message: String
    let latencyMS: Int?

    enum CodingKeys: String, CodingKey {
        case available, address, message
        case latencyMS = "latency_ms"
    }

    init(available: Bool, address: String? = nil, message: String, latencyMS: Int? = nil) {
        self.available = available
        self.address = address
        self.message = message
        self.latencyMS = latencyMS
    }
}

struct RoutingConfig: Codable, Equatable {
    var version: Int
    var mode: RoutingMode
    var defaultAction: RoutingAction
    var applications: [RoutingApplication]
    var systemSOCKS: RoutingSOCKSConfig
    var clashListenPort: Int

    enum CodingKeys: String, CodingKey {
        case version, mode, applications
        case defaultAction = "default_action"
        case systemSOCKS = "system_socks"
        case clashListenPort = "clash_listen_port"
    }

    static let initial = RoutingConfig(
        version: 2,
        mode: .independent,
        defaultAction: .systemDirect,
        applications: [],
        systemSOCKS: RoutingSOCKSConfig(),
        clashListenPort: 17890)
}

struct RoutingInterfaceInfo: Codable, Equatable {
    let name: String
    let ipv4: String
    let ipv6: String?
}

struct RoutingRuntime: Decodable, Equatable {
    let enabled: Bool
    let state: String
    let mode: RoutingMode?
    let message: String?
    let moduleInterface: RoutingInterfaceInfo?
    let systemInterface: String?
    let socksAddress: String?
    let startedAt: Date?

    enum CodingKeys: String, CodingKey {
        case enabled, state, mode, message
        case moduleInterface = "module_interface"
        case systemInterface = "system_interface"
        case socksAddress = "socks_address"
        case startedAt = "started_at"
    }

    static let stopped = RoutingRuntime(
        enabled: false,
        state: "stopped",
        mode: nil,
        message: nil,
        moduleInterface: nil,
        systemInterface: nil,
        socksAddress: nil,
        startedAt: nil)
}

struct RoutingCapabilities: Decodable, Equatable {
    let coreAvailable: Bool
    let coreVersion: String?
    let corePath: String?
    let serviceInstalled: Bool
    let serviceCurrent: Bool

    enum CodingKeys: String, CodingKey {
        case coreAvailable = "core_available"
        case coreVersion = "core_version"
        case corePath = "core_path"
        case serviceInstalled = "service_installed"
        case serviceCurrent = "service_current"
    }
}

struct RoutingSnapshot: Decodable {
    let config: RoutingConfig
    let runtime: RoutingRuntime
    let capabilities: RoutingCapabilities
}

struct RoutingConflict: Decodable, Identifiable, Equatable {
    let interface: String
    let destinations: [String]
    let detail: String

    var id: String { "\(interface)|\(destinations.joined(separator: ","))" }
}

struct RoutingPreflight: Decodable, Equatable {
    let ready: Bool
    let coreAvailable: Bool
    let coreVersion: String?
    let moduleInterface: RoutingInterfaceInfo?
    let systemInterface: String?
    let conflicts: [RoutingConflict]
    let issues: [String]
    let warnings: [String]

    enum CodingKeys: String, CodingKey {
        case ready, conflicts, issues, warnings
        case coreAvailable = "core_available"
        case coreVersion = "core_version"
        case moduleInterface = "module_interface"
        case systemInterface = "system_interface"
    }
}

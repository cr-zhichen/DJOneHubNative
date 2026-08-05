import Foundation

// MARK: - 通用

/// GET /api/health 的响应模型
struct HealthStatus: Decodable {
    let ok: Bool
    let port: String?
    let esimAvailable: Bool?
    let discoveryError: String?
    let usbDevice: USBDeviceStatus?

    enum CodingKeys: String, CodingKey {
        case ok, port
        case esimAvailable = "esim_available"
        case discoveryError = "discovery_error"
        case usbDevice = "usb_device"
    }
}

struct USBDeviceStatus: Decodable {
    let product: String?
    let vendor: String?
    let vendorID: String?
    let productID: String?
    let locationID: String?
    let speed: String?
    let mode: String?
    let interfaces: [USBInterface]?

    enum CodingKeys: String, CodingKey {
        case product, vendor, speed, mode, interfaces
        case vendorID = "vendor_id"
        case productID = "product_id"
        case locationID = "location_id"
    }
}

struct USBInterface: Decodable {
    let number: Int?
    let classID: Int?
    let subclass: Int?
    let protocolID: Int?
    let endpoints: Int?

    enum CodingKeys: String, CodingKey {
        case number, endpoints
        case classID = "class"
        case subclass, protocolID = "protocol"
    }
}

// MARK: - 模块状态

/// GET /api/status 的响应模型
struct DeviceStatus: Decodable {
    let imei: String?
    let firmware: String?
    let iccid: String?
    let imsi: String?
    let operatorName: String?
    let simInserted: Bool?
    let signalDbm: Int?
    let signalRsrp: Int?
    let signalRsrq: Int?
    let signalSinr: Int?
    let radioBand: String?
    let regStatus: Int?
    let regStatusText: String?
    let psAttached: Bool?
    let lac: String?
    let cellID: String?
    let apn: String?
    let imsStatus: Int?
    let networkMode: String?
    let networkDuplex: String?
    let usbnetMode: Int?
    let operatingMode: Int?
    let hardwareStatus: String?
    let discoveryError: String?
    let usbDevice: USBDeviceStatus?

    enum CodingKeys: String, CodingKey {
        case imei, firmware, iccid, imsi
        case operatorName = "operator"
        case simInserted = "sim_inserted"
        case signalDbm = "signal_dbm"
        case signalRsrp = "signal_rsrp"
        case signalRsrq = "signal_rsrq"
        case signalSinr = "signal_sinr"
        case radioBand = "radio_band"
        case regStatus = "reg_status"
        case regStatusText = "reg_status_text"
        case psAttached = "ps_attached"
        case lac
        case cellID = "cell_id"
        case apn, imsStatus = "ims_status"
        case networkMode = "network_mode"
        case networkDuplex = "network_duplex"
        case usbnetMode = "usbnet_mode"
        case operatingMode = "operating_mode"
        case hardwareStatus = "hardware_status"
        case discoveryError = "discovery_error"
        case usbDevice = "usb_device"
    }
}

// MARK: - 短信

/// GET /api/sms：顶层是数组
struct SMSItem: Decodable, Identifiable {
    let sender: String?
    let content: String?
    let code: String?
    let timestamp: Date
    let moduleStorage: String?
    let moduleIndex: Int?
    let archived: Bool?
    let direction: String?

    var id: String { "\(timestamp.timeIntervalSince1970)|\(sender ?? "")|\(content ?? "")" }
    /// 短信当前仍在模块/SIM 存储中（可精确定位删除）
    var isFromModule: Bool { (moduleIndex ?? 0) > 0 && archived != true }
    /// 已持久化到本机
    var isArchived: Bool { archived == true }
    /// 是否为发送的短信
    var isOutgoing: Bool { direction == "out" }

    enum CodingKeys: String, CodingKey {
        case sender, content, code, timestamp, archived, direction
        case moduleStorage = "module_storage"
        case moduleIndex = "module_index"
    }
}

/// GET /api/sms/storage
struct SMSStorageUsage: Decodable {
    let used: Int
    let total: Int
}

struct SMSStorageResponse: Decodable {
    let usage: [String: SMSStorageUsage]?
}

/// POST /api/sms/delete
struct SMSDeleteRequest: Encodable {
    let storage: String?
    let index: Int
    let sender: String?
    let content: String?
    let timestamp: Date?

    init(storage: String? = nil, index: Int = 0, sender: String? = nil, content: String? = nil, timestamp: Date? = nil) {
        self.storage = storage
        self.index = index
        self.sender = sender
        self.content = content
        self.timestamp = timestamp
    }
}

struct SMSDeleteResult: Decodable {
    let deleted: Bool
}

/// POST /api/sms/adopt
struct SMSAdoptRequest: Encodable {
    let enabled: Bool
}

/// POST /api/sms/clear
struct SMSClearRequest: Encodable {
    let sm: Bool
    let me: Bool
    let local: Bool
}

struct SMSClearResult: Decodable {
    let cleared: Bool
}

/// GET /api/sms/status
struct SMSStatus: Decodable {
    let count: Int?
    let polling: Bool?
    let pollIntervalS: Int?
    let autoCleanupME: Bool?
    let lastPoll: Date?
    let lastPollError: String?

    enum CodingKeys: String, CodingKey {
        case count, polling
        case pollIntervalS = "poll_interval_s"
        case autoCleanupME = "auto_cleanup_me"
        case lastPoll = "last_poll"
        case lastPollError = "last_poll_error"
    }
}

/// POST /api/sms/send
struct SMSSendRequest: Encodable {
    let phone: String
    let message: String
}

struct SMSSendResult: Decodable {
    let sent: Bool
    let segments: Int?
}

/// POST /api/sms/refresh（202）
struct SMSRefreshResult: Decodable {
    let accepted: Bool
    let count: Int?
}

// MARK: - AT

struct ATRequest: Encodable {
    let command: String
}

struct ATResult: Decodable {
    let response: String?
}

// MARK: - 网络

struct PDPContext: Decodable {
    let id: Int?
    let pdn: String?
    let apn: String?
}

struct MACInterface: Decodable {
    let name: String?
    let status: String?
    let ipv4: String?
    let kind: String?
}

struct DefaultRoute: Decodable {
    let interface: String?
    let gateway: String?
}

/// GET /api/network
struct NetworkDiagnostic: Decodable {
    let usbnetMode: String?
    let usbcfg: String?
    let pdpContexts: [PDPContext]?
    let activeContexts: [Int]?
    let pdpAddresses: [String]?
    let macInterfaces: [MACInterface]?
    let defaultRoute: DefaultRoute?
    let usbNetworkPresent: Bool?
    let errors: [String: String]?

    enum CodingKeys: String, CodingKey {
        case usbcfg
        case usbnetMode = "usbnet_mode"
        case pdpContexts = "pdp_contexts"
        case activeContexts = "active_contexts"
        case pdpAddresses = "pdp_addresses"
        case macInterfaces = "mac_interfaces"
        case defaultRoute = "default_route"
        case usbNetworkPresent = "usb_network_present"
        case errors
    }
}

/// GET /api/network/traffic
struct TrafficSnapshot: Decodable {
    let available: Bool
    let interface: String?
    let rxBytes: UInt64?
    let txBytes: UInt64?
    let sessionRX: UInt64?
    let sessionTX: UInt64?
    let sessionTotal: UInt64?
    let sampledAtMS: Int64?
    let error: String?

    enum CodingKeys: String, CodingKey {
        case available, interface, error
        case rxBytes = "rx_bytes"
        case txBytes = "tx_bytes"
        case sessionRX = "session_rx_bytes"
        case sessionTX = "session_tx_bytes"
        case sessionTotal = "session_total_bytes"
        case sampledAtMS = "sampled_at_ms"
    }
}

/// POST /api/network/check-4g、check-proxy
struct CheckResult: Decodable {
    let ok: Bool
    let summary: String?
    let detail: String?
}

/// POST /api/network/usbnet
struct USBNetRequest: Encodable {
    let mode: Int
}

struct USBNetResult: Decodable {
    let mode: Int?
    let response: String?
    let needsReboot: Bool?

    enum CodingKeys: String, CodingKey {
        case mode, response
        case needsReboot = "needs_reboot"
    }
}

/// POST /api/network/reboot-module（202）
struct RebootResult: Decodable {
    let accepted: Bool
    let response: String?
}

// MARK: - 语音与通话

struct CallStatus: Decodable, Equatable {
    let state: String
    let number: String?
    let incoming: Bool?
    let active: Bool?

    var isIdle: Bool { state == "idle" }
    var isIncoming: Bool { state == "incoming" }
    var isActive: Bool { state == "active" }
}

struct CallDialRequest: Encodable {
    let number: String
}

struct CallActionResult: Decodable {
    let accepted: Bool
    let response: String?
}

struct CallClearResult: Decodable {
    let ok: Bool
}

struct CallDeleteRequest: Encodable {
    let id: String
}

// MARK: - 通话记录

/// GET /api/calls：顶层是数组
struct CallRecord: Decodable, Identifiable {
    let id: String
    let direction: String
    let number: String?
    let answered: Bool
    let startedAt: Date
    let endedAt: Date
    let duration: Int

    var isIncoming: Bool { direction == "in" }
    var isMissed: Bool { isIncoming && !answered }

    enum CodingKeys: String, CodingKey {
        case id, direction, number, answered, duration
        case startedAt = "started_at"
        case endedAt = "ended_at"
    }
}

struct VoiceEnableRequest: Encodable {
    let enabled: Bool
}

struct VoiceEnableResult: Decodable {
    let accepted: Bool
    let enabled: Bool
    let needsUnplug: Bool?

    enum CodingKeys: String, CodingKey {
        case accepted, enabled
        case needsUnplug = "needs_unplug"
    }
}

struct VoiceEnabledResponse: Decodable {
    let enabled: Bool
}

/// GET /api/network/services
struct NetworkService: Decodable, Identifiable {
    let name: String
    let device: String?
    let port: String?
    let usb: Bool?
    let module: Bool?

    var id: String { name }
}

/// PUT /api/network/services-order
struct ServicesOrderRequest: Encodable {
    let services: [String]
}

// MARK: - eSIM

struct EUICCInfo: Decodable {
    let aid: String?
    let eid: String?
    let spec: String?
    let freeNvram: String?
    let freeNvramBytes: Int32?
    let firmware: String?
    let manufacturer: String?
    let sasAccreditationNumber: String?

    enum CodingKeys: String, CodingKey {
        case aid, eid, spec, firmware, manufacturer
        case freeNvram = "free_nvram"
        case freeNvramBytes = "free_nvram_bytes"
        case sasAccreditationNumber = "sas_accreditation_number"
    }
}

struct ChipInfo: Decodable {
    let eids: [EUICCInfo]?
    let skuName: String?
    let serialNumber: String?
    let firmware: String?

    enum CodingKeys: String, CodingKey {
        case eids
        case skuName = "sku_name"
        case serialNumber = "serial_number"
        case firmware
    }
}

struct ProfileItem: Decodable, Identifiable {
    let iccid: String
    let name: String?
    let serviceProviderName: String?
    let state: Int?
    let stateText: String?
    let classText: String?

    var id: String { iccid }
    var isEnabled: Bool { state == 1 }

    enum CodingKeys: String, CodingKey {
        case iccid, name, state
        case serviceProviderName = "service_provider_name"
        case stateText = "state_text"
        case classText = "class_text"
    }
}

struct EUICCProfiles: Decodable {
    let eid: String?
    let aidHex: String?
    let profiles: [ProfileItem]?

    enum CodingKeys: String, CodingKey {
        case eid
        case aidHex = "aid_hex"
        case profiles
    }
}

/// GET /api/esim
struct ESIMOverview: Decodable {
    let cardType: String?
    let message: String?
    let chipInfo: ChipInfo?
    let profiles: [EUICCProfiles]?

    enum CodingKeys: String, CodingKey {
        case message
        case cardType = "card_type"
        case chipInfo = "chip_info"
        case profiles
    }
}

/// GET /api/esim/notes
struct ProfileNote: Decodable {
    let label: String?
    let phone: String?
    let tags: String?
}

struct NotesResponse: Decodable {
    let notes: [String: ProfileNote]?
}

/// PUT /api/esim/notes
struct SaveNoteRequest: Encodable {
    let iccid: String
    let label: String
    let phone: String
    let tags: String
}

struct MessageResponse: Decodable {
    let message: String?
}

/// GET /api/esim/module-notes
struct ModuleProfileNote: Decodable {
    let index: Int?
    let iccid: String?
    let label: String?
    let phone: String?
    let tags: String?
}

struct ModuleNotesResponse: Decodable {
    let notes: [String: ModuleProfileNote]?
    let used: Int?
    let total: Int?
}

/// PUT /api/esim/module-notes
struct SaveModuleNoteRequest: Encodable {
    let iccid: String
    let label: String
    let phone: String
    let tags: String
}

/// POST /api/esim/phonebook/probe
struct PhonebookProbeResult: Decodable {
    let storageSupported: Bool?
    let storageSelected: Bool?
    let readSupported: Bool?
    let writeSupported: Bool?
    let storageStatus: String?
    let responses: [String: String]?

    enum CodingKeys: String, CodingKey {
        case responses
        case storageSupported = "storage_supported"
        case storageSelected = "storage_selected"
        case readSupported = "read_supported"
        case writeSupported = "write_supported"
        case storageStatus = "storage_status"
    }
}

/// POST /api/esim/switch
struct ESIMSwitchRequest: Encodable {
    let iccid: String
    let aid: String?
}

struct ESIMSwitchResult: Decodable {
    let switchAccepted: Bool?
    let phase: String?
    let targetIccid: String?
    let recoveryPending: Bool?
    let moduleRebootRequested: Bool?
    let moduleRebootResponse: String?
    let moduleRebootWarning: String?
    let reconnectWaitSeconds: Int?

    enum CodingKeys: String, CodingKey {
        case phase
        case switchAccepted = "switch_accepted"
        case targetIccid = "target_iccid"
        case recoveryPending = "recovery_pending"
        case moduleRebootRequested = "module_reboot_requested"
        case moduleRebootResponse = "module_reboot_response"
        case moduleRebootWarning = "module_reboot_warning"
        case reconnectWaitSeconds = "reconnect_wait_seconds"
    }
}

/// PATCH /api/esim/profile
struct ESIMRenameRequest: Encodable {
    let iccid: String
    let aid: String?
    let name: String
}

/// DELETE /api/esim/profile
struct ESIMDeleteRequest: Encodable {
    let iccid: String
    let aid: String?
}

/// 删除/下载 Profile 的结果（Go 端无 json tag，键名首字母大写；SpaceDelta 可为 null）
struct SpaceDelta: Decodable {
    let direction: String?
    let bytes: Int?
}

struct ESIMProfileResult: Decodable {
    let warning: String?
    let warningCode: String?
    let spaceDelta: SpaceDelta?

    enum CodingKeys: String, CodingKey {
        case warning = "Warning"
        case warningCode = "WarningCode"
        case spaceDelta = "SpaceDelta"
    }
}

/// POST /api/esim/download
struct ESIMDownloadRequest: Encodable {
    let smdp: String
    let matchingID: String?
    let confirmationCode: String?
    let aid: String?
    let imei: String?

    enum CodingKeys: String, CodingKey {
        case smdp, aid, imei
        case matchingID = "matching_id"
        case confirmationCode = "confirmation_code"
    }
}

/// GET /api/esim/health
struct ESIMHealth: Decodable {
    let ok: Bool?
    let cardType: String?
    let message: String?
    let activeProfile: ProfileItem?
    let moduleIccid: String?
    let imsi: String?
    let operatorName: String?
    let registration: String?
    let registered: Bool?
    let signalDbm: Int?
    let networkMode: String?

    enum CodingKeys: String, CodingKey {
        case ok, message, registration, registered, imsi
        case cardType = "card_type"
        case activeProfile = "active_profile"
        case moduleIccid = "module_iccid"
        case operatorName = "operator"
        case signalDbm = "signal_dbm"
        case networkMode = "network_mode"
    }
}

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

// MARK: - 模块定位

enum GPSState: String, Decodable {
    case checking
    case disabled
    case searching
    case fixed
    case stale
    case unavailable
    case unsupported
    case error
    case unknown

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        let rawValue = try container.decode(String.self)
        self = GPSState(rawValue: rawValue) ?? .unknown
    }
}

/// GET /api/gps/status，以及定位启停接口的响应模型。
struct GPSStatus: Decodable, Equatable {
    let supported: Bool?
    let enabled: Bool
    let state: GPSState
    let latitude: Double?
    let longitude: Double?
    let altitude: Double?
    let hdop: Double?
    let fixType: Int?
    let satellites: Int?
    let utc: String?
    let date: String?
    let updatedAt: Date?
    let checkedAt: Date?
    let error: String?
    let pollIntervalSeconds: Int?

    enum CodingKeys: String, CodingKey {
        case supported, enabled, state, latitude, longitude, altitude, hdop
        case fixType = "fix_type"
        case satellites, utc, date
        case updatedAt = "updated_at"
        case checkedAt = "checked_at"
        case error
        case pollIntervalSeconds = "poll_interval_s"
    }
}

// MARK: - 短信

/// GET /api/sms：顶层是数组
struct SMSItem: Decodable, Identifiable {
    /// 后端生成的稳定逻辑消息 ID，用于精确删除；旧后端可能不提供。
    let backendID: String?
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
        case backendID = "id"
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
    let id: String?
    let storage: String?
    let index: Int
    let sender: String?
    let content: String?
    let timestamp: Date?

    init(
        id: String? = nil,
        storage: String? = nil,
        index: Int = 0,
        sender: String? = nil,
        content: String? = nil,
        timestamp: Date? = nil
    ) {
        self.id = id
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

/// POST /api/sms/delete-sender
struct SMSDeleteSenderRequest: Encodable {
    let sender: String
}

struct SMSDeleteSenderResult: Decodable {
    let deleted: Bool
    let deletedCount: Int

    enum CodingKeys: String, CodingKey {
        case deleted
        case deletedCount = "deleted_count"
    }
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
    let warning: String?
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

struct CallModeDownloadSource: Decodable, Identifiable, Equatable {
    let id: String
    let name: String
    let detail: String
    let trusted: Bool
}

struct CallModeRestoreNotice: Decodable, Equatable {
    let backupID: String
    let backupSavedAt: Date
    let restoredAt: Date
    let safetyBackupID: String?
    let changed: Bool

    enum CodingKeys: String, CodingKey {
        case changed
        case backupID = "backup_id"
        case backupSavedAt = "backup_saved_at"
        case restoredAt = "restored_at"
        case safetyBackupID = "safety_backup_id"
    }
}

struct CallModeStatus: Decodable, Equatable {
    let state: String
    let summary: String
    let detail: String?
    let adbEnabled: Bool
    let interfacesReady: Bool
    let adbAuthorizationRequired: Bool
    let uacEnabled: Bool
    let voiceConfigured: Bool
    let runtimeDownloaded: Bool
    let runtimeVersion: String
    let runtimePath: String
    let downloadedBytes: Int64
    let totalBytes: Int64
    let source: String?
    let canEnable: Bool
    let canDownload: Bool
    let canRetry: Bool
    let requiresADBAuthorizationConfirmation: Bool
    let requiresUSBConfirmation: Bool
    let requiresDownloadConfirmation: Bool
    let backupPath: String?
    let lastRestore: CallModeRestoreNotice?
    let updatedAt: Date
    let sources: [CallModeDownloadSource]
    let upstream: String

    var isReady: Bool { state == "ready" }
    var isBusy: Bool {
        [
            "authorizing_adb", "enabling_usb", "enabling_voice", "restarting", "verifying_usb",
            "downloading", "preparing", "restoring_usb", "restarting_restore",
        ].contains(state)
    }
    var downloadProgress: Double {
        guard totalBytes > 0 else { return 0 }
        return min(1, max(0, Double(downloadedBytes) / Double(totalBytes)))
    }

    enum CodingKeys: String, CodingKey {
        case state, summary, detail, source, sources, upstream
        case adbEnabled = "adb_enabled"
        case interfacesReady = "interfaces_ready"
        case adbAuthorizationRequired = "adb_authorization_required"
        case uacEnabled = "uac_enabled"
        case voiceConfigured = "voice_configured"
        case runtimeDownloaded = "runtime_downloaded"
        case runtimeVersion = "runtime_version"
        case runtimePath = "runtime_path"
        case downloadedBytes = "downloaded_bytes"
        case totalBytes = "total_bytes"
        case canEnable = "can_enable"
        case canDownload = "can_download"
        case canRetry = "can_retry"
        case requiresADBAuthorizationConfirmation = "requires_adb_authorization_confirmation"
        case requiresUSBConfirmation = "requires_usb_confirmation"
        case requiresDownloadConfirmation = "requires_download_confirmation"
        case backupPath = "backup_path"
        case lastRestore = "last_restore"
        case updatedAt = "updated_at"
    }
}

struct CallModeUSBBackupSummary: Decodable, Identifiable, Equatable {
    let id: String
    let fileName: String
    let schemaVersion: Int
    let savedAt: Date
    let reason: String
    let moduleIMEI: String?
    let firmware: String?
    let vendorID: String?
    let productID: String?
    let flags: [Int]?
    let adbEnabled: Bool
    let uacEnabled: Bool
    let voiceIncluded: Bool
    let imsConfigured: Bool
    let imported: Bool
    let valid: Bool
    let restorable: Bool
    let detail: String?

    enum CodingKeys: String, CodingKey {
        case id, reason, flags, imported, valid, restorable, detail, firmware
        case fileName = "file_name"
        case schemaVersion = "schema_version"
        case savedAt = "saved_at"
        case moduleIMEI = "module_imei"
        case vendorID = "vendor_id"
        case productID = "product_id"
        case adbEnabled = "adb_enabled"
        case uacEnabled = "uac_enabled"
        case voiceIncluded = "voice_included"
        case imsConfigured = "ims_configured"
    }
}

struct CallModeUSBBackupListResponse: Decodable {
    let backups: [CallModeUSBBackupSummary]
}

struct CallModeUSBBackupImportResponse: Decodable {
    let backup: CallModeUSBBackupSummary
}

struct CallModeUSBBackupDeleteResponse: Decodable {
    let deleted: Bool
    let backupID: String

    enum CodingKeys: String, CodingKey {
        case deleted
        case backupID = "backup_id"
    }
}

struct CallModeEnableRequest: Encodable {
    let confirm: Bool
    let confirmADBAuthorization: Bool

    enum CodingKeys: String, CodingKey {
        case confirm
        case confirmADBAuthorization = "confirm_adb_authorization"
    }
}

struct CallModeDownloadRequest: Encodable {
    let confirm: Bool
    let source: String
}

struct CallModeRestoreRequest: Encodable {
    let confirm: Bool
    let backupID: String

    enum CodingKeys: String, CodingKey {
        case confirm
        case backupID = "backup_id"
    }
}

struct CallModeBackupDeleteRequest: Encodable {
    let confirm: Bool
    let backupID: String

    enum CodingKeys: String, CodingKey {
        case confirm
        case backupID = "backup_id"
    }
}

struct CallAudioStartResult: Decodable {
    let started: Bool
    let runtimeVersion: String

    enum CodingKeys: String, CodingKey {
        case started
        case runtimeVersion = "runtime_version"
    }
}

struct CallAudioStopResult: Decodable {
    let stopped: Bool
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
    let specGuess: String?
    let specConfidence: String?
    let infoSource: String?
    let infoVersion: String?
    let infoError: String?
    let defaultSMDPAddress: String?
    let rootDSAddress: String?

    enum CodingKeys: String, CodingKey {
        case aid, eid, spec, firmware, manufacturer
        case freeNvram = "free_nvram"
        case freeNvramBytes = "free_nvram_bytes"
        case sasAccreditationNumber = "sas_accreditation_number"
        case specGuess = "spec_guess"
        case specConfidence = "spec_confidence"
        case infoSource = "info_source"
        case infoVersion = "info_version"
        case infoError = "info_error"
        case defaultSMDPAddress = "default_smdp_address"
        case rootDSAddress = "root_ds_address"
    }
}

struct CardIdentity: Decodable {
    let brand: String?
    let model: String?
    let hardwareRevision: String?
    let source: String?
    let confidence: String?
    let ruleID: String?
    let sourceURL: String?
    let sourceVersion: String?
    let evidence: [CardIdentityEvidence]?

    enum CodingKeys: String, CodingKey {
        case brand, model, source, confidence, evidence
        case hardwareRevision = "hardware_revision"
        case ruleID = "rule_id"
        case sourceURL = "source_url"
        case sourceVersion = "source_version"
    }
}

struct CardIdentityEvidence: Decodable {
    let kind: String
    let name: String
    let url: String
    let version: String
}

struct ChipInfo: Decodable {
    let eids: [EUICCInfo]?
    let skuName: String?
    let serialNumber: String?
    let firmware: String?
    let identity: CardIdentity?

    enum CodingKeys: String, CodingKey {
        case eids
        case skuName = "sku_name"
        case serialNumber = "serial_number"
        case firmware, identity
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
    var displayName: String {
        if let name = name?.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty {
            return name
        }
        if let provider = serviceProviderName?.trimmingCharacters(in: .whitespacesAndNewlines), !provider.isEmpty {
            return provider
        }
        return iccid.isEmpty ? "Profile" : "Profile · …\(iccid.suffix(6))"
    }

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
    let readState: String?
    let readError: String?
    let profiles: [ProfileItem]?

    enum CodingKeys: String, CodingKey {
        case eid
        case aidHex = "aid_hex"
        case readState = "read_state"
        case readError = "read_error"
        case profiles
    }
}

struct ESIMCapabilities: Decodable {
    let canRefresh: Bool
    let canDownload: Bool
    let canSwitch: Bool
    let canRename: Bool
    let canDelete: Bool
    let canProbePhonebook: Bool
    let vendorSerialAvailable: Bool

    enum CodingKeys: String, CodingKey {
        case canRefresh = "can_refresh"
        case canDownload = "can_download"
        case canSwitch = "can_switch"
        case canRename = "can_rename"
        case canDelete = "can_delete"
        case canProbePhonebook = "can_probe_phonebook"
        case vendorSerialAvailable = "vendor_serial_available"
    }
}

/// GET /api/esim
struct ESIMOverview: Decodable {
    let cardType: String?
    let message: String?
    let chipInfo: ChipInfo?
    let profiles: [EUICCProfiles]?
    let capabilities: ESIMCapabilities?
    let updatedAt: Date?
    let operation: ESIMOperationSnapshot?

    enum CodingKeys: String, CodingKey {
        case message, capabilities, operation
        case cardType = "card_type"
        case chipInfo = "chip_info"
        case profiles
        case updatedAt = "updated_at"
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
    let storage: String?
    let storageSupported: Bool?
    let storageSelected: Bool?
    let readSupported: Bool?
    let writeSupported: Bool?
    let storageStatus: String?
    let responses: [String: String]?

    enum CodingKeys: String, CodingKey {
        case storage, responses
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
    let operation: ESIMOperationSnapshot?

    enum CodingKeys: String, CodingKey {
        case phase, operation
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

/// 删除/下载 Profile 的结果。
struct SpaceDelta: Decodable {
    let direction: String?
    let bytes: Int?
}

struct ESIMProfileResult: Decodable {
    let warning: String?
    let warningCode: String?
    let spaceDelta: SpaceDelta?

    enum CodingKeys: String, CodingKey {
        case warning
        case warningCode = "warning_code"
        case spaceDelta = "space_delta"
    }
}

struct ESIMOperationResult: Decodable {
    let warning: String?
    let warningCode: String?
    let spaceDelta: SpaceDelta?

    enum CodingKeys: String, CodingKey {
        case warning
        case warningCode = "warning_code"
        case spaceDelta = "space_delta"
    }
}

struct ESIMOperationSnapshot: Decodable, Identifiable {
    let id: String
    let kind: String
    let state: String
    let step: String?
    let message: String?
    let progress: Int
    let targetICCID: String?
    let errorCode: String?
    let error: String?
    let recoverable: Bool?
    let refreshAfterSeconds: Int?
    let result: ESIMOperationResult?
    let startedAt: Date
    let updatedAt: Date
    let finishedAt: Date?

    var isActive: Bool { state == "queued" || state == "running" }
    var isSuccessful: Bool { state == "succeeded" }
    var hasWarning: Bool { state == "warning" }
    var isFailed: Bool { state == "failed" }

    enum CodingKeys: String, CodingKey {
        case id, kind, state, step, message, progress, error, recoverable, result
        case targetICCID = "target_iccid"
        case errorCode = "error_code"
        case refreshAfterSeconds = "refresh_after_seconds"
        case startedAt = "started_at"
        case updatedAt = "updated_at"
        case finishedAt = "finished_at"
    }
}

struct ESIMOperationResponse: Decodable {
    let operation: ESIMOperationSnapshot?
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
    let state: String?
    let message: String?
    let activeProfile: ProfileItem?
    let moduleIccid: String?
    let imsi: String?
    let operatorName: String?
    let registration: String?
    let registered: Bool?
    let signalDbm: Int?
    let networkMode: String?
    let profileMatchesModule: Bool?
    let operation: ESIMOperationSnapshot?

    enum CodingKeys: String, CodingKey {
        case ok, state, message, registration, registered, imsi, operation
        case cardType = "card_type"
        case activeProfile = "active_profile"
        case moduleIccid = "module_iccid"
        case operatorName = "operator"
        case signalDbm = "signal_dbm"
        case networkMode = "network_mode"
        case profileMatchesModule = "profile_matches_module"
    }
}

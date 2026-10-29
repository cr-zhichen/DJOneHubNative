import Foundation

enum PermissionAuthorizationState: Equatable {
    case authorized
    case limited
    case notDetermined
    case denied
    case restricted
    case unavailable
}

enum NotificationAuthorizationKind: Equatable {
    case authorized
    case provisional
    case ephemeral
    case notDetermined
    case denied
    case unavailable
}

enum PermissionImportance: Equatable {
    case required
    case optional
}

enum PermissionCenterLevel: Equatable {
    case healthy
    case warning
    case neutral
}

enum PermissionCenterAction: Equatable {
    case request
    case openSettings
    case none
}

struct PermissionCenterPresentation: Equatable {
    let status: String
    let level: PermissionCenterLevel
    let action: PermissionCenterAction
}

enum PermissionCenterPolicy {
    static func notificationState(
        authorization: NotificationAuthorizationKind,
        alertsEnabled: Bool
    ) -> PermissionAuthorizationState {
        switch authorization {
        case .authorized:
            return alertsEnabled ? .authorized : .limited
        case .provisional, .ephemeral:
            return .limited
        case .notDetermined:
            return .notDetermined
        case .denied:
            return .denied
        case .unavailable:
            return .unavailable
        }
    }

    static func presentation(
        state: PermissionAuthorizationState,
        importance: PermissionImportance
    ) -> PermissionCenterPresentation {
        switch state {
        case .authorized:
            return .init(status: "已授权", level: .healthy, action: .none)
        case .limited:
            return .init(
                status: "提醒受限",
                level: importance == .required ? .warning : .neutral,
                action: .openSettings)
        case .notDetermined:
            return .init(
                status: importance == .required ? "待授权" : "按需授权",
                level: importance == .required ? .warning : .neutral,
                action: .request)
        case .denied:
            return .init(
                status: "已拒绝",
                level: importance == .required ? .warning : .neutral,
                action: .openSettings)
        case .restricted:
            return .init(
                status: "受系统限制",
                level: importance == .required ? .warning : .neutral,
                action: .none)
        case .unavailable:
            return .init(
                status: "当前不可用",
                level: importance == .required ? .warning : .neutral,
                action: .none)
        }
    }
}

struct PermissionCenterItem: Identifiable, Equatable {
    let id: String
    let title: String
    let detail: String
    let systemImage: String
    let state: PermissionAuthorizationState
    let importance: PermissionImportance

    var presentation: PermissionCenterPresentation {
        PermissionCenterPolicy.presentation(state: state, importance: importance)
    }
}

struct PermissionCenterSnapshot: Equatable {
    let items: [PermissionCenterItem]

    init(
        microphone: PermissionAuthorizationState,
        speechRecognition: PermissionAuthorizationState,
        notifications: PermissionAuthorizationState,
        camera: PermissionAuthorizationState
    ) {
        items = [
            .init(
                id: "microphone",
                title: "麦克风",
                detail: "用于本机通话音频输入；未授权时无法发送本机麦克风声音。",
                systemImage: "mic.fill",
                state: microphone,
                importance: .required),
            .init(
                id: "speech",
                title: "语音识别",
                detail: "仅在启用本地 ASR 时使用，通话音频默认不长期保存。",
                systemImage: "waveform.and.mic",
                state: speechRecognition,
                importance: .optional),
            .init(
                id: "notifications",
                title: "通知",
                detail: "用于新短信、来电和重要运行状态提醒。",
                systemImage: "bell.fill",
                state: notifications,
                importance: .required),
            .init(
                id: "camera",
                title: "相机",
                detail: "仅在扫描 eSIM 二维码时使用，画面只在本机处理。",
                systemImage: "camera.fill",
                state: camera,
                importance: .optional),
        ]
    }

    var overallLevel: PermissionCenterLevel {
        if items.contains(where: { $0.presentation.level == .warning }) {
            return .warning
        }
        if items.contains(where: { $0.presentation.level == .healthy }) {
            return .healthy
        }
        return .neutral
    }
}

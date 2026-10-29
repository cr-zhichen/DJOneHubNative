import AppKit
import AVFoundation
import Foundation
import Speech
import UserNotifications

@MainActor
final class SystemPermissionCenter: ObservableObject {
    @Published private(set) var snapshot = PermissionCenterSnapshot(
        microphone: .unavailable,
        speechRecognition: .unavailable,
        notifications: .unavailable,
        camera: .unavailable)
    @Published private(set) var isRefreshing = false
    @Published private(set) var requestingID: String?
    private var refreshGeneration = 0

    func refresh() {
        refreshGeneration += 1
        let generation = refreshGeneration
        isRefreshing = true
        Task {
            let notificationSettings = await UNUserNotificationCenter.current().notificationSettings()
            guard generation == refreshGeneration else { return }
            snapshot = PermissionCenterSnapshot(
                microphone: Self.captureState(for: .audio),
                speechRecognition: Self.speechState(),
                notifications: Self.notificationState(notificationSettings),
                camera: Self.captureState(for: .video))
            isRefreshing = false
        }
    }

    func perform(_ item: PermissionCenterItem) {
        switch item.presentation.action {
        case .request:
            request(item)
        case .openSettings:
            openSettings(for: item.id)
        case .none:
            break
        }
    }

    private func request(_ item: PermissionCenterItem) {
        guard requestingID == nil else { return }
        refreshGeneration += 1
        requestingID = item.id
        Task {
            switch item.id {
            case "microphone":
                _ = await AVCaptureDevice.requestAccess(for: .audio)
            case "camera":
                _ = await AVCaptureDevice.requestAccess(for: .video)
            case "speech":
                _ = await withCheckedContinuation { continuation in
                    SFSpeechRecognizer.requestAuthorization { status in
                        continuation.resume(returning: status)
                    }
                }
            case "notifications":
                _ = try? await UNUserNotificationCenter.current()
                    .requestAuthorization(options: [.alert, .sound, .badge])
            default:
                break
            }
            requestingID = nil
            refresh()
        }
    }

    private func openSettings(for id: String) {
        let rawURL: String
        switch id {
        case "microphone":
            rawURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone"
        case "speech":
            rawURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_SpeechRecognition"
        case "camera":
            rawURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_Camera"
        case "notifications":
            rawURL = "x-apple.systempreferences:com.apple.Notifications-Settings.extension"
        default:
            rawURL = "x-apple.systempreferences:com.apple.preference.security"
        }
        guard let url = URL(string: rawURL) else { return }
        NSWorkspace.shared.open(url)
    }

    private static func captureState(for mediaType: AVMediaType) -> PermissionAuthorizationState {
        switch AVCaptureDevice.authorizationStatus(for: mediaType) {
        case .authorized: return .authorized
        case .notDetermined: return .notDetermined
        case .denied: return .denied
        case .restricted: return .restricted
        @unknown default: return .unavailable
        }
    }

    private static func speechState() -> PermissionAuthorizationState {
        switch SFSpeechRecognizer.authorizationStatus() {
        case .authorized: return .authorized
        case .notDetermined: return .notDetermined
        case .denied: return .denied
        case .restricted: return .restricted
        @unknown default: return .unavailable
        }
    }

    private static func notificationState(
        _ settings: UNNotificationSettings
    ) -> PermissionAuthorizationState {
        let authorization: NotificationAuthorizationKind
        switch settings.authorizationStatus {
        case .authorized: authorization = .authorized
        case .provisional: authorization = .provisional
        case .ephemeral: authorization = .ephemeral
        case .notDetermined: authorization = .notDetermined
        case .denied: authorization = .denied
        @unknown default: authorization = .unavailable
        }
        return PermissionCenterPolicy.notificationState(
            authorization: authorization,
            alertsEnabled: settings.alertSetting != .disabled)
    }
}

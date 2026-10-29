import Foundation

@main
struct PermissionCenterProbe {
    static func main() {
        expect(
            PermissionCenterPolicy.presentation(
                state: .authorized,
                importance: .required) == .init(
                    status: "已授权",
                    level: .healthy,
                    action: .none),
            "authorized permission must be healthy without action")

        expect(
            PermissionCenterPolicy.presentation(
                state: .notDetermined,
                importance: .required) == .init(
                    status: "待授权",
                    level: .warning,
                    action: .request),
            "undetermined required permission must be requestable")

        expect(
            PermissionCenterPolicy.presentation(
                state: .denied,
                importance: .required) == .init(
                    status: "已拒绝",
                    level: .warning,
                    action: .openSettings),
            "denied required permission must direct to settings")

        expect(
            PermissionCenterPolicy.presentation(
                state: .restricted,
                importance: .required) == .init(
                    status: "受系统限制",
                    level: .warning,
                    action: .none),
            "restricted permission is policy-managed and must not offer settings")

        expect(
            PermissionCenterPolicy.presentation(
                state: .limited,
                importance: .required) == .init(
                    status: "提醒受限",
                    level: .warning,
                    action: .openSettings),
            "limited required permission must remain visible and actionable")

        expect(
            PermissionCenterPolicy.notificationState(
                authorization: .authorized,
                alertsEnabled: true) == .authorized,
            "authorized notifications with alerts must be healthy")
        expect(
            PermissionCenterPolicy.notificationState(
                authorization: .authorized,
                alertsEnabled: false) == .limited,
            "disabled notification alerts must not be reported as authorized")
        expect(
            PermissionCenterPolicy.notificationState(
                authorization: .provisional,
                alertsEnabled: true) == .limited,
            "provisional notifications must be reported as limited")
        expect(
            PermissionCenterPolicy.notificationState(
                authorization: .ephemeral,
                alertsEnabled: true) == .limited,
            "ephemeral notifications must be reported as limited")

        expect(
            PermissionCenterPolicy.presentation(
                state: .notDetermined,
                importance: .optional) == .init(
                    status: "按需授权",
                    level: .neutral,
                    action: .request),
            "optional permission must not create a warning before use")

        expect(
            PermissionCenterPolicy.presentation(
                state: .unavailable,
                importance: .optional) == .init(
                    status: "当前不可用",
                    level: .neutral,
                    action: .none),
            "unavailable optional permission must be neutral and non-actionable")

        let snapshot = PermissionCenterSnapshot(
            microphone: .authorized,
            speechRecognition: .notDetermined,
            notifications: .denied,
            camera: .notDetermined)

        expect(snapshot.items.map(\.id) == ["microphone", "speech", "notifications", "camera"],
               "permission center must keep stable item order")
        expect(snapshot.items.first(where: { $0.id == "speech" })?.presentation.level == .neutral,
               "speech recognition is optional until the ASR workflow requests it")
        expect(snapshot.items.first(where: { $0.id == "notifications" })?.presentation.level == .warning,
               "notifications denial must be visible because alerts are a core workflow")
        expect(snapshot.overallLevel == .warning,
               "overall level must reflect required permission attention")

        print("PASS permission center policy")
    }

    private static func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
        guard condition() else {
            fputs("FAIL: \(message)\n", stderr)
            exit(1)
        }
    }
}

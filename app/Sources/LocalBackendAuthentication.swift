import Foundation
import Security

/// Ephemeral credential shared only by the native app and its child backend.
/// It is regenerated for every app process and is never written to disk.
enum LocalBackendAuthentication {
    static let appToken: String = {
        var bytes = [UInt8](repeating: 0, count: 32)
        let status = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        precondition(status == errSecSuccess, "Unable to generate the local backend credential")
        return bytes.map { String(format: "%02x", $0) }.joined()
    }()

    static let headerName = "X-DJOneHub-App-Token"
}

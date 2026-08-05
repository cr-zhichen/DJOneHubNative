import Foundation

// 原型链路验证：与 App 完全相同的 URLProtocol + APIClient
UnixSocketURLProtocol.socketPath = CommandLine.arguments[1]

struct SMSItem: Decodable { let sender: String?; let content: String? }
struct CheckResult: Decodable { let ok: Bool?; let summary: String?; let detail: String? }

let semaphore = DispatchSemaphore(value: 0)
Task {
    do {
        let client = APIClient()

        let health: HealthStatus = try await client.get("api/health")
        print("health: ok=\(health.ok) demo=\(health.demo ?? false) port=\(health.port ?? "-")")

        let sms: [SMSItem] = try await client.get("api/sms")
        print("sms GET: \(sms.count) items")

        let check: CheckResult = try await client.send("api/network/check-4g")
        print("network check-4g POST: ok=\(check.ok ?? false) summary=\(check.summary ?? "-")")

        struct ATRequest: Encodable { let command: String }
        struct ATResult: Decodable { let response: String?; let ok: Bool? }
        let at: ATResult = try await client.send("api/at", body: ATRequest(command: "AT"))
        print("at POST: ok=\(at.ok ?? false) resp=\(at.response ?? "-")")

        exit(0)
    } catch {
        print("ERROR: \(error)")
        exit(1)
    }
}
semaphore.wait()

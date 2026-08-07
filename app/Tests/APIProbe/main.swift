import Foundation

// 无硬件环境下的 API 验证：验证服务连通、模型解码、错误响应解码
// 真机验证时可在有硬件环境下运行完整功能测试
UnixSocketURLProtocol.socketPath = CommandLine.arguments[1]

struct CheckError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}

func require(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    guard condition() else { throw CheckError(message) }
}

let semaphore = DispatchSemaphore(value: 0)
DispatchQueue.global().asyncAfter(deadline: .now() + 60) {
    print("\n超时退出")
    exit(2)
}

Task {
    do {
        let client = APIClient()
        var failures: [String] = []

        func check(_ name: String, _ block: @escaping () async throws -> Void) async {
            do { try await block(); print("✓ \(name)") }
            catch { failures.append("\(name): \(error.localizedDescription)"); print("✗ \(name): \(error.localizedDescription)") }
        }

        await check("health") {
            let h: HealthStatus = try await client.get("api/health")
            try require(h.ok, "health not ok")
        }
        await check("status") {
            let s: DeviceStatus = try await client.get("api/status")
            // 无硬件时走兜底结构，有硬件时走完整结构，两者都应可解码
            print("  operator=\(s.operatorName ?? "-") sim=\(s.simInserted ?? false) err=\(s.discoveryError ?? "-")")
        }
        await check("sms list") {
            let list: [SMSItem] = try await client.get("api/sms")
            print("  \(list.count) items")
        }
        await check("sms status") {
            let st: SMSStatus = try await client.get("api/sms/status")
            try require(st.count != nil, "count missing")
        }
        await check("sms send (无硬件应报错)") {
            do {
                let _: SMSSendResult = try await client.send("api/sms/send", body: SMSSendRequest(phone: "10086", message: "test"))
                // 如果硬件存在则发送成功也 OK
            } catch let error as APIError {
                try require(error.statusCode >= 400, "unexpected status \(error.statusCode)")
            }
        }
        await check("at (无硬件应报错)") {
            do {
                let _: ATResult = try await client.send("api/at", body: ATRequest(command: "AT"))
            } catch let error as APIError {
                try require(error.statusCode >= 400, "unexpected status \(error.statusCode)")
            }
        }
        await check("network diagnostic") {
            let d: NetworkDiagnostic = try await client.get("api/network")
            print("  usbnet=\(d.usbnetMode ?? "-") ifaces=\(d.macInterfaces?.count ?? 0)")
        }
        await check("network traffic") {
            let t: TrafficSnapshot = try await client.get("api/network/traffic")
            print("  available=\(t.available) iface=\(t.interface ?? "-")")
        }
        await check("network check-4g") {
            let r: CheckResult = try await client.send("api/network/check-4g")
            print("  ok=\(r.ok) \(r.summary ?? "-")")
        }
        await check("routing config defaults to stopped runtime") {
            let r: RoutingSnapshot = try await client.get("api/routing")
            try require(!r.runtime.enabled, "routing must not auto-start")
            print("  mode=\(r.config.mode.rawValue) rules=\(r.config.applications.count) core=\(r.capabilities.coreAvailable)")
        }
        await check("routing preflight") {
            let r: RoutingPreflight = try await client.send("api/routing/preflight")
            print("  ready=\(r.ready) conflicts=\(r.conflicts.count) issues=\(r.issues.count)")
        }
        await check("esim overview (无硬件应 503)") {
            do {
                let e: ESIMOverview = try await client.get("api/esim")
                print("  card_type=\(e.cardType ?? "-") profiles=\(e.profiles?.flatMap { $0.profiles ?? [] }.count ?? 0)")
            } catch let error as APIError {
                try require(error.statusCode >= 400, "unexpected status \(error.statusCode)")
            }
        }
        await check("esim notes (磁盘持久化应可用)") {
            let r: NotesResponse = try await client.get("api/esim/notes")
            print("  \(r.notes?.count ?? 0) notes")
        }

        if failures.isEmpty {
            print("\n无硬件环境验证全部通过")
            exit(0)
        } else {
            print("\n\(failures.count) 项失败：")
            for f in failures { print("  - \(f)") }
            exit(1)
        }
    }
}
semaphore.wait()

import Foundation

/// 基于 URLSession + UnixSocketURLProtocol 的 API 客户端。
/// 请求走 http+unix:// 协议，socket 路径由 BackendProcess 统一提供。
struct APIClient {
    private static let base = URL(string: "http+unix://djonehub")!

    private let session: URLSession
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()

    init(timeoutInterval: TimeInterval = 30) {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [UnixSocketURLProtocol.self]
        config.timeoutIntervalForRequest = timeoutInterval
        config.timeoutIntervalForResource = 60
        session = URLSession(configuration: config)

        // Go 端 time.Time 输出 RFC3339Nano，可能带小数秒
        let withFraction = ISO8601DateFormatter()
        withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let str = try container.decode(String.self)
            if let d = withFraction.date(from: str) { return d }
            if let d = plain.date(from: str) { return d }
            throw DecodingError.dataCorruptedError(
                in: container, debugDescription: "无法解析时间：\(str)")
        }

        encoder.dateEncodingStrategy = .iso8601
    }

    func get<T: Decodable>(_ path: String, as type: T.Type = T.self) async throws -> T {
        let url = Self.base.appendingPathComponent(path)
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        let (data, response) = try await session.data(for: request)
        try Self.validate(response)
        return try decoder.decode(T.self, from: data)
    }

    func send<T: Decodable>(
        _ path: String,
        method: String = "POST",
        body: Encodable? = nil,
        as type: T.Type = T.self
    ) async throws -> T {
        let url = Self.base.appendingPathComponent(path)
        var request = URLRequest(url: url)
        request.httpMethod = method
        if let body {
            request.httpBody = try encoder.encode(body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, response) = try await session.data(for: request)
        try Self.validate(response)
        return try decoder.decode(T.self, from: data)
    }

    func send(_ path: String, method: String = "POST", body: Encodable? = nil) async throws {
        let url = Self.base.appendingPathComponent(path)
        var request = URLRequest(url: url)
        request.httpMethod = method
        if let body {
            request.httpBody = try encoder.encode(body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (_, response) = try await session.data(for: request)
        try Self.validate(response)
    }

    private static func validate(_ response: URLResponse) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            throw APIError.httpStatus(http.statusCode)
        }
    }
}

enum APIError: LocalizedError {
    case httpStatus(Int)

    var errorDescription: String? {
        switch self {
        case .httpStatus(let code): return "服务返回错误（HTTP \(code)）"
        }
    }
}

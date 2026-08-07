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
        config.timeoutIntervalForResource = max(60, timeoutInterval)
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
        try Self.validate(response, data: data)
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
        try Self.validate(response, data: data)
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
        let (data, response) = try await session.data(for: request)
        try Self.validate(response, data: data)
    }

    private static func validate(_ response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            if let payload = try? JSONDecoder().decode(APIErrorPayload.self, from: data),
               !payload.error.isEmpty {
                throw APIError.server(http.statusCode, payload.error)
            }
            throw APIError.httpStatus(http.statusCode)
        }
    }
}

private struct APIErrorPayload: Decodable {
    let error: String
}

enum APIError: LocalizedError {
    case httpStatus(Int)
    case server(Int, String)

    var statusCode: Int {
        switch self {
        case .httpStatus(let code), .server(let code, _): return code
        }
    }

    var errorDescription: String? {
        switch self {
        case .httpStatus(let code): return "服务返回错误（HTTP \(code)）"
        case .server(_, let message): return message
        }
    }
}

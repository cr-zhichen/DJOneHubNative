import Foundation
import Darwin

/// 让 URLSession 支持 http+unix:// 协议，通过 Unix domain socket 访问本机后端。
///
/// URL 形如 http+unix://djonehub/api/status，其中 host 部分无实际含义，
/// 真实 socket 路径由 `socketPath` 静态属性提供。
final class UnixSocketURLProtocol: URLProtocol {
    /// 由 BackendProcess 在初始化时设置
    static var socketPath: String = ""

    private var socketFD: Int32 = -1
    private let ioQueue = DispatchQueue(label: "unix-socket-protocol")

    override class func canInit(with request: URLRequest) -> Bool {
        request.url?.scheme == "http+unix"
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override class func requestIsCacheEquivalent(_ a: URLRequest, to b: URLRequest) -> Bool {
        false
    }

    override func startLoading() {
        let request = request
        let socketPath = Self.socketPath
        ioQueue.async { [weak self] in
            guard let self else { return }
            self.perform(request: request, socketPath: socketPath)
        }
    }

    override func stopLoading() {
        if socketFD >= 0 {
            Darwin.close(socketFD)
            socketFD = -1
        }
    }

    // MARK: - 请求执行

    private func perform(request: URLRequest, socketPath: String) {
        guard let fd = connectToUnixSocket(socketPath) else {
            fail(URLError(.cannotConnectToHost))
            return
        }
        socketFD = fd

        guard let wire = buildRequestData(request) else {
            fail(URLError(.badURL))
            return
        }

        var sent = 0
        while sent < wire.count {
            let n = wire.withUnsafeBytes { ptr -> Int in
                Darwin.send(fd, ptr.baseAddress!.advanced(by: sent), wire.count - sent, 0)
            }
            if n <= 0 { fail(URLError(.networkConnectionLost)); return }
            sent += n
        }

        guard let (status, headers, body) = readResponse(fd: fd) else {
            fail(URLError(.badServerResponse))
            return
        }

        let url = request.url ?? URL(string: "http+unix://djonehub/")!
        let response = HTTPURLResponse(
            url: url, statusCode: status, httpVersion: "HTTP/1.1",
            headerFields: headers
        )
        client?.urlProtocol(self, didReceive: response!, cacheStoragePolicy: .notAllowed)
        if let body, !body.isEmpty {
            client?.urlProtocol(self, didLoad: body)
        }
        client?.urlProtocolDidFinishLoading(self)
    }

    private func fail(_ error: URLError) {
        client?.urlProtocol(self, didFailWithError: error)
    }

    // MARK: - 请求构建

    private func buildRequestData(_ request: URLRequest) -> Data? {
        guard let url = request.url else { return nil }
        let method = request.httpMethod ?? "GET"
        var path = url.path
        if let query = url.query { path += "?\(query)" }

        var lines = ["\(method) \(path) HTTP/1.1"]
        lines.append("Host: djonehub.local")
        lines.append("Connection: close")

        let defaultHeaders = Set(["Host", "Connection", "Content-Length"])
        for (key, value) in request.allHTTPHeaderFields ?? [:] {
            guard !defaultHeaders.contains(key) else { continue }
            lines.append("\(key): \(value)")
        }

        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = Data()
            var chunk = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&chunk, maxLength: chunk.count)
                if n < 0 { stream.close(); return nil }
                if n == 0 { break }
                buffer.append(chunk, count: n)
            }
            stream.close()
            body = buffer
        }
        if !body.isEmpty {
            lines.append("Content-Length: \(body.count)")
        }

        var data = Data(lines.joined(separator: "\r\n").utf8)
        data.append(contentsOf: [0x0D, 0x0A, 0x0D, 0x0A])
        if !body.isEmpty { data.append(body) }
        return data
    }

    // MARK: - 响应解析

    /// 返回 (状态码, 头部字典, 响应体)；失败返回 nil
    private func readResponse(fd: Int32) -> (Int, [String: String], Data?)? {
        var buf = [UInt8](repeating: 0, count: 65536)
        var received = Data()

        func readMore() -> Bool {
            let n = Darwin.recv(fd, &buf, buf.count, 0)
            if n <= 0 { return false }
            received.append(buf, count: n)
            return true
        }

        // 1. 读头部，直到 \r\n\r\n
        var headerEnd: Range<Data.Index>?
        while true {
            headerEnd = received.range(of: Data([0x0D, 0x0A, 0x0D, 0x0A]))
            if headerEnd != nil { break }
            if !readMore() { return nil }
        }

        guard let headerData = headerEnd else { return nil }
        let head = String(data: received[..<headerData.lowerBound], encoding: .utf8) ?? ""
        let headLines = head.components(separatedBy: "\r\n")

        guard !headLines.isEmpty else { return nil }
        let statusParts = headLines[0].split(separator: " ", maxSplits: 2).map(String.init)
        guard statusParts.count >= 2, let status = Int(statusParts[1]) else { return nil }

        var headers: [String: String] = [:]
        for line in headLines.dropFirst() {
            if let colon = line.firstIndex(of: ":") {
                let key = line[..<colon].trimmingCharacters(in: .whitespaces)
                let value = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
                headers[key.lowercased()] = value
            }
        }

        // 2. 读 body：支持 Content-Length 与 chunked
        let bodyStart = headerEnd!.upperBound
        if let cl = headers["content-length"], let length = Int(cl) {
            var body = Data()
            if received.count - bodyStart > 0 {
                body = received.subdata(in: bodyStart..<received.count)
            }
            while body.count < length {
                if !readMore() { return nil }
                body = received.subdata(in: bodyStart..<received.count)
            }
            return (status, headers, body.prefix(length))
        }

        if headers["transfer-encoding"]?.lowercased().contains("chunked") == true {
            var body = Data()
            var pos = bodyStart
            while true {
                if let lineEnd = received[pos...].range(of: Data([0x0D, 0x0A])) {
                    let sizeLine = String(data: received[pos..<lineEnd.lowerBound], encoding: .utf8)?
                        .trimmingCharacters(in: .whitespacesAndNewlines)
                    guard let sizeHex = sizeLine, let chunkSize = Int(sizeHex, radix: 16) else {
                        return nil
                    }
                    if chunkSize == 0 { break }
                    let chunkStart = lineEnd.upperBound
                    let chunkEnd = chunkStart + chunkSize
                    while received.count < chunkEnd + 2 {
                        if !readMore() { return nil }
                    }
                    body.append(received.subdata(in: chunkStart..<chunkEnd))
                    pos = chunkEnd + 2
                } else {
                    if !readMore() { return nil }
                }
            }
            return (status, headers, body)
        }

        // 无 body
        return (status, headers, nil)
    }

    // MARK: - Unix socket 连接

    private func connectToUnixSocket(_ path: String) -> Int32? {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { return nil }

        let pathBytes = Array(path.utf8)
        guard pathBytes.count < MemoryLayout<sockaddr_un>.size - 1 else {
            Darwin.close(fd)
            return nil
        }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutablePointer(to: &addr.sun_path) { pathPtr in
            let base = UnsafeMutableRawPointer(pathPtr)
            for (i, byte) in pathBytes.enumerated() {
                base.storeBytes(of: byte, toByteOffset: i, as: UInt8.self)
            }
            base.storeBytes(of: 0, toByteOffset: pathBytes.count, as: UInt8.self)
        }

        let result = withUnsafePointer(to: &addr) { ptr -> Int32 in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                Darwin.connect(fd, sa, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard result == 0 else {
            Darwin.close(fd)
            return nil
        }
        return fd
    }
}

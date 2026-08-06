import Foundation
import Combine
import Darwin

/// GitHub Release 信息（用于检查更新）
struct GitHubRelease: Decodable {
    let tagName: String
    let prerelease: Bool
    let publishedAt: Date?
    let htmlUrl: URL?
    let assets: [GitHubAsset]?

    enum CodingKeys: String, CodingKey {
        case tagName = "tag_name"
        case prerelease
        case publishedAt = "published_at"
        case htmlUrl = "html_url"
        case assets
    }
}

struct GitHubAsset: Decodable {
    let name: String
    let browserDownloadUrl: URL?

    enum CodingKeys: String, CodingKey {
        case name
        case browserDownloadUrl = "browser_download_url"
    }
}

/// Semantic Versioning 2.0.0 precedence (build metadata does not affect ordering).
struct SemanticVersion: Comparable {
    private enum PrereleaseIdentifier: Equatable {
        case number(Int)
        case text(String)
    }

    private let major: Int
    private let minor: Int
    private let patch: Int
    private let prerelease: [PrereleaseIdentifier]?

    init?(_ rawValue: String) {
        let normalized = Self.normalizedString(rawValue)
        let buildParts = normalized.split(separator: "+", maxSplits: 1, omittingEmptySubsequences: false)
        guard let precedence = buildParts.first, !precedence.isEmpty else { return nil }
        if buildParts.count == 2 {
            guard Self.validDotSeparatedIdentifiers(buildParts[1]) else { return nil }
        }

        let precedenceParts = precedence.split(separator: "-", maxSplits: 1, omittingEmptySubsequences: false)
        let core = precedenceParts[0].split(separator: ".", omittingEmptySubsequences: false)
        guard core.count == 3,
              let major = Self.parseNumericIdentifier(core[0]),
              let minor = Self.parseNumericIdentifier(core[1]),
              let patch = Self.parseNumericIdentifier(core[2]) else {
            return nil
        }

        self.major = major
        self.minor = minor
        self.patch = patch

        if precedenceParts.count == 2 {
            let rawIdentifiers = precedenceParts[1].split(separator: ".", omittingEmptySubsequences: false)
            guard !rawIdentifiers.isEmpty else { return nil }

            var identifiers: [PrereleaseIdentifier] = []
            identifiers.reserveCapacity(rawIdentifiers.count)
            for identifier in rawIdentifiers {
                guard !identifier.isEmpty, Self.validIdentifier(identifier) else { return nil }
                if Self.isASCIIInteger(identifier) {
                    guard let value = Self.parseNumericIdentifier(identifier) else { return nil }
                    identifiers.append(.number(value))
                } else {
                    identifiers.append(.text(String(identifier)))
                }
            }
            prerelease = identifiers
        } else {
            prerelease = nil
        }
    }

    static func normalizedString(_ rawValue: String) -> String {
        var value = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.first == "v" || value.first == "V" {
            value.removeFirst()
        }
        return value
    }

    var isStable: Bool {
        prerelease == nil
    }

    var isBetaOrReleaseCandidate: Bool {
        guard let firstIdentifier = prerelease?.first else { return false }
        switch firstIdentifier {
        case let .text(value):
            return value.lowercased() == "beta" || value.lowercased() == "rc"
        case .number:
            return false
        }
    }

    static func < (lhs: SemanticVersion, rhs: SemanticVersion) -> Bool {
        if lhs.major != rhs.major { return lhs.major < rhs.major }
        if lhs.minor != rhs.minor { return lhs.minor < rhs.minor }
        if lhs.patch != rhs.patch { return lhs.patch < rhs.patch }

        switch (lhs.prerelease, rhs.prerelease) {
        case (nil, nil):
            return false
        case (nil, _):
            return false
        case (_, nil):
            return true
        case let (lhsIdentifiers?, rhsIdentifiers?):
            for index in 0..<min(lhsIdentifiers.count, rhsIdentifiers.count) {
                let left = lhsIdentifiers[index]
                let right = rhsIdentifiers[index]
                if left == right { continue }

                switch (left, right) {
                case let (.number(leftValue), .number(rightValue)):
                    return leftValue < rightValue
                case (.number, .text):
                    return true
                case (.text, .number):
                    return false
                case let (.text(leftValue), .text(rightValue)):
                    return leftValue < rightValue
                }
            }
            return lhsIdentifiers.count < rhsIdentifiers.count
        }
    }

    private static func parseNumericIdentifier(_ value: Substring) -> Int? {
        guard isASCIIInteger(value), !(value.count > 1 && value.first == "0") else { return nil }
        return Int(value)
    }

    private static func isASCIIInteger(_ value: Substring) -> Bool {
        !value.isEmpty && value.unicodeScalars.allSatisfy { (48...57).contains($0.value) }
    }

    private static func validIdentifier(_ value: Substring) -> Bool {
        !value.isEmpty && value.unicodeScalars.allSatisfy {
            (48...57).contains($0.value)
                || (65...90).contains($0.value)
                || (97...122).contains($0.value)
                || $0.value == 45
        }
    }

    private static func validDotSeparatedIdentifiers(_ value: Substring) -> Bool {
        let identifiers = value.split(separator: ".", omittingEmptySubsequences: false)
        return !identifiers.isEmpty && identifiers.allSatisfy(validIdentifier)
    }
}

/// 更新检查：查询 GitHub Releases，区分正式版与测试版（prerelease）。
@MainActor
final class UpdateChecker: ObservableObject {
    static let shared = UpdateChecker()

    /// 更新渠道
    enum Channel: String, CaseIterable, Identifiable {
        case stable = "正式版"
        case beta = "测试版"

        var id: String { rawValue }

        /// 渠道说明
        var detail: String {
            switch self {
            case .stable: return "仅检查正式发布版本"
            case .beta: return "检查正式版、Beta 与 RC"
            }
        }
    }

    private let repo = "cr-zhichen/DJOneHubNative"

    @Published var channel: Channel {
        didSet {
            UserDefaults.standard.set(channel.rawValue, forKey: Self.channelKey)
        }
    }
    @Published var autoCheck: Bool {
        didSet {
            UserDefaults.standard.set(autoCheck, forKey: Self.autoCheckKey)
        }
    }
    @Published var checking = false
    @Published var latestRelease: GitHubRelease?
    @Published var lastError: String?
    @Published private(set) var lastCheckDate: Date?
    /// 发现新版本且未被跳过时置位，由界面弹窗展示
    @Published var pendingUpdate: GitHubRelease?

    private static let channelKey = "updateChannel"
    private static let autoCheckKey = "updateAutoCheck"
    private static let lastCheckKey = "updateLastCheck"
    private static let skippedKey = "updateSkippedVersions"

    /// 用户选择"跳过本次更新"的版本号集合（持久化）
    private var skippedVersions: Set<String> {
        didSet {
            UserDefaults.standard.set(Array(skippedVersions), forKey: Self.skippedKey)
        }
    }

    init() {
        let defaults = UserDefaults.standard
        channel = Channel(rawValue: defaults.string(forKey: Self.channelKey) ?? "") ?? .stable
        if defaults.object(forKey: Self.autoCheckKey) == nil {
            defaults.set(true, forKey: Self.autoCheckKey)
        }
        autoCheck = defaults.bool(forKey: Self.autoCheckKey)
        lastCheckDate = defaults.object(forKey: Self.lastCheckKey) as? Date
        skippedVersions = Set(defaults.stringArray(forKey: Self.skippedKey) ?? [])
    }

    // MARK: - 版本信息

    var currentVersion: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "-"
    }

    var buildNumber: String {
        Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "-"
    }

    /// 是否存在可用更新（已被跳过的版本不计入）
    var updateAvailable: Bool {
        guard hasNewerVersion else { return false }
        guard let latest = latestRelease else { return false }
        let latestVersion = SemanticVersion.normalizedString(latest.tagName)
        return !skippedVersions.contains(latestVersion)
    }

    /// GitHub 上是否存在比当前更新的版本（与跳过状态无关）
    var hasNewerVersion: Bool {
        guard let latest = latestRelease,
              let latestVersion = SemanticVersion(latest.tagName),
              let installedVersion = SemanticVersion(currentVersion) else {
            return false
        }
        return latestVersion > installedVersion
    }

    /// 最新版本号（去掉 v 前缀）
    var latestVersionString: String {
        guard let latest = latestRelease else { return "-" }
        return SemanticVersion.normalizedString(latest.tagName)
    }

    /// 适合当前机器的 DMG 下载地址；找不到时回退到版本页面
    var downloadURL: URL? {
        guard let release = latestRelease else { return nil }
        let archSuffix = isAppleSilicon ? "-arm64" : "-x86_64"
        let dmgAssets = (release.assets ?? []).filter { $0.name.hasSuffix(".dmg") }
        let preferred = dmgAssets.first { $0.name.contains(archSuffix) } ?? dmgAssets.first
        return preferred?.browserDownloadUrl ?? release.htmlUrl
    }

    // MARK: - 检查更新

    /// 启动时自动检查（autoCheck 开启时每次启动都检查）；发现更新时弹窗提醒
    func autoCheckIfNeeded() async {
        guard autoCheck else { return }
        await check(prompt: true)
    }

    /// 检查更新。prompt 为 true 时（仅启动检查）发现更新会置位 pendingUpdate 触发弹窗；
    /// 手动检查（点击版本行、切换渠道）不弹窗。
    func check(prompt: Bool = false) async {
        guard !checking else { return }
        checking = true
        lastError = nil
        defer { checking = false }
        var url = URL(string: "https://api.github.com/repos/\(repo)/releases")!
        url.append(queryItems: [URLQueryItem(name: "per_page", value: "100")])
        var request = URLRequest(url: url)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("DJOneHub/\(currentVersion)", forHTTPHeaderField: "User-Agent")

        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            if let http = response as? HTTPURLResponse, http.statusCode != 200 {
                throw URLError(.badServerResponse)
            }
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .custom { decoder in
                let container = try decoder.singleValueContainer()
                let s = try container.decode(String.self)
                let formatter = ISO8601DateFormatter()
                if let date = formatter.date(from: s) { return date }
                formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
                if let date = formatter.date(from: s) { return date }
                throw DecodingError.dataCorruptedError(in: container, debugDescription: "无法解析日期: \(s)")
            }
            let releases = try decoder.decode([GitHubRelease].self, from: data)

            let matched = Self.selectLatestRelease(from: releases, for: channel)
            latestRelease = matched
            lastCheckDate = Date()
            UserDefaults.standard.set(lastCheckDate, forKey: Self.lastCheckKey)
            if updateAvailable && prompt {
                pendingUpdate = matched
            }
        } catch {
            lastError = error.localizedDescription
        }
    }

    // MARK: - 更新弹窗操作

    /// 关闭弹窗（暂不更新，下次启动仍会提醒）
    func dismissUpdate() {
        pendingUpdate = nil
    }

    /// 跳过本次更新：该版本不再提醒
    func skipUpdate(_ release: GitHubRelease) {
        skippedVersions.insert(SemanticVersion.normalizedString(release.tagName))
        pendingUpdate = nil
    }

    /// 清除最新版本的跳过标记并弹出更新弹窗（点击"最新版本"行时调用）
    func unskipAndPrompt() {
        guard let latest = latestRelease else { return }
        skippedVersions.remove(SemanticVersion.normalizedString(latest.tagName))
        pendingUpdate = latest
    }

    // MARK: - 工具

    private var isAppleSilicon: Bool {
        var info = utsname()
        uname(&info)
        let machine = withUnsafeBytes(of: &info.machine) { raw -> String in
            String(cString: raw.bindMemory(to: CChar.self).baseAddress!)
        }
        return machine == "arm64"
    }

    /// 正式版渠道只接受稳定版；测试版渠道接受稳定版、Beta 与 RC，并按 SemVer 取最高版本。
    nonisolated static func selectLatestRelease(
        from releases: [GitHubRelease],
        for channel: Channel
    ) -> GitHubRelease? {
        releases
            .compactMap { release -> (release: GitHubRelease, version: SemanticVersion)? in
                guard let version = SemanticVersion(release.tagName) else { return nil }
                return (release, version)
            }
            .filter { candidate in
                switch channel {
                case .stable:
                    return !candidate.release.prerelease && candidate.version.isStable
                case .beta:
                    if candidate.release.prerelease {
                        return candidate.version.isBetaOrReleaseCandidate
                    }
                    return candidate.version.isStable
                }
            }
            .max { $0.version < $1.version }?
            .release
    }
}

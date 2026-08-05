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
            case .beta: return "包含测试版与预览版"
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
        let latestVersion = latest.tagName.replacingOccurrences(of: "v", with: "")
        return !skippedVersions.contains(latestVersion)
    }

    /// GitHub 上是否存在比当前更新的版本（与跳过状态无关）
    var hasNewerVersion: Bool {
        guard let latest = latestRelease else { return false }
        let latestVersion = latest.tagName.replacingOccurrences(of: "v", with: "")
        return compareVersions(latestVersion, currentVersion) > 0
    }

    /// 最新版本号（去掉 v 前缀）
    var latestVersionString: String {
        guard let latest = latestRelease else { return "-" }
        return latest.tagName.replacingOccurrences(of: "v", with: "")
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
        url.append(queryItems: [URLQueryItem(name: "per_page", value: "20")])
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

            let matched = releases.first { release in
                channel == .beta ? true : !release.prerelease
            }
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
        skippedVersions.insert(release.tagName.replacingOccurrences(of: "v", with: ""))
        pendingUpdate = nil
    }

    /// 清除最新版本的跳过标记并弹出更新弹窗（点击"最新版本"行时调用）
    func unskipAndPrompt() {
        guard let latest = latestRelease else { return }
        skippedVersions.remove(latest.tagName.replacingOccurrences(of: "v", with: ""))
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

    /// 简单版本号比较（忽略预发布后缀）：返回 1 / 0 / -1
    private func compareVersions(_ a: String, _ b: String) -> Int {
        let pa = a.split(separator: ".").compactMap { Int($0) }
        let pb = b.split(separator: ".").compactMap { Int($0) }
        for i in 0..<max(pa.count, pb.count) {
            let x = i < pa.count ? pa[i] : 0
            let y = i < pb.count ? pb[i] : 0
            if x != y { return x > y ? 1 : -1 }
        }
        return 0
    }
}

import AppKit
import Combine
import Foundation

@MainActor
final class RoutingStore: ObservableObject {
    enum LoadPhase: Equatable {
        case idle
        case loading
        case loaded
        case failed(String)
    }

    @Published var config: RoutingConfig = .initial {
        didSet {
            guard !isApplyingSnapshot, oldValue != config else { return }
            isDirty = true
            configRevision &+= 1
            invalidatePreflight()
            schedulePreflight()
        }
    }
    @Published private(set) var runtime: RoutingRuntime = .stopped
    @Published private(set) var capabilities: RoutingCapabilities?
    @Published private(set) var preflight: RoutingPreflight?
    @Published private(set) var loadPhase: LoadPhase = .idle
    @Published private(set) var isSaving = false
    @Published private(set) var isSwitching = false
    @Published private(set) var isChecking = false
    @Published private(set) var isDirty = false
    @Published private(set) var pendingEnabled: Bool?
    @Published var errorMessage: String?

    private var isApplyingSnapshot = false
    private var configRevision = 0
    private var preflightRevision: Int?
    private var preflightGeneration = 0
    private var pollTask: Task<Void, Never>?
    private var preflightDebounceTask: Task<Void, Never>?

    deinit {
        pollTask?.cancel()
        preflightDebounceTask?.cancel()
    }

    var isLoaded: Bool {
        loadPhase == .loaded
    }

    var isLoading: Bool {
        loadPhase == .loading
    }

    var isConfigurationLocked: Bool {
        runtime.enabled || isSwitching || isSaving
    }

    var toggleIsOn: Bool {
        pendingEnabled ?? runtime.enabled
    }

    var usesSystemSOCKS: Bool {
        config.applications.contains { $0.action == .systemSOCKS }
    }

    var hasCurrentPreflight: Bool {
        preflight != nil && preflightRevision == configRevision
    }

    var canEnable: Bool {
        isLoaded
            && !isDirty
            && !isSaving
            && !isSwitching
            && capabilities?.coreAvailable == true
            && hasCurrentPreflight
            && preflight?.ready == true
    }

    var enableBlockReason: String? {
        guard !runtime.enabled else { return nil }
        guard isLoaded else { return "配置尚未加载" }
        if isDirty { return "请先保存配置" }
        if capabilities?.coreAvailable != true { return "网络核心不可用" }
        if isChecking { return "正在检测可用性…" }
        if !hasCurrentPreflight { return "请先完成可用性检测" }
        if preflight?.ready != true { return "请先处理检测问题" }
        return nil
    }

    var clashConfigurationYAML: String {
        """
        proxies:
          - name: DJOneHub-4G
            type: socks5
            server: 127.0.0.1
            port: \(config.clashListenPort)
            udp: true
        """
    }

    func load() {
        guard loadPhase == .idle || isLoadFailure else { return }
        loadPhase = .loading
        Task {
            do {
                let snapshot: RoutingSnapshot = try await APIClient().get("api/routing")
                apply(snapshot, includeConfig: true)
                loadPhase = .loaded
                await checkPreflight()
            } catch {
                loadPhase = .failed(error.localizedDescription)
            }
        }
    }

    func beginPolling() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                guard !Task.isCancelled else { return }
                guard self?.isLoaded == true else { continue }
                await self?.refreshRuntime()
            }
        }
    }

    func endPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    func refreshRuntime() async {
        guard isLoaded else { return }
        do {
            let snapshot: RoutingSnapshot = try await APIClient().get("api/routing")
            apply(snapshot, includeConfig: !isDirty)
        } catch {
            if runtime.enabled {
                errorMessage = error.localizedDescription
            }
        }
    }

    func save() async -> Bool {
        guard isLoaded, !runtime.enabled, !isSwitching, isDirty else { return false }
        isSaving = true
        defer { isSaving = false }
        do {
            let snapshot: RoutingSnapshot = try await APIClient().send(
                "api/routing/config", method: "PUT", body: config)
            apply(snapshot, includeConfig: true)
            await checkPreflight()
            return true
        } catch {
            errorMessage = error.localizedDescription
            return false
        }
    }

    func setEnabled(_ enabled: Bool) {
        guard isLoaded, !isSwitching, runtime.enabled != enabled else { return }
        if enabled, !canEnable {
            errorMessage = enableBlockReason ?? "当前配置尚未通过可用性检测。"
            return
        }

        isSwitching = true
        pendingEnabled = enabled
        errorMessage = nil
        Task {
            defer {
                isSwitching = false
                pendingEnabled = nil
            }
            do {
                if enabled {
                    let snapshot: RoutingSnapshot = try await APIClient(timeoutInterval: 210).send(
                        "api/routing/start")
                    apply(snapshot, includeConfig: false)
                } else {
                    // 即使网络核心已丢失，也必须允许用户请求后端停止当前会话。
                    let snapshot: RoutingSnapshot = try await APIClient().send("api/routing/stop")
                    apply(snapshot, includeConfig: false)
                }
                await checkPreflight()
            } catch {
                errorMessage = error.localizedDescription
                await refreshRuntime()
                await checkPreflight()
            }
        }
    }

    func checkPreflight() async {
        preflightDebounceTask?.cancel()
        preflightDebounceTask = nil
        await performPreflight()
    }

    func addApplication(url: URL) {
        let canonicalURL = url.resolvingSymlinksInPath()
        guard let bundle = Bundle(url: canonicalURL), let executableURL = bundle.executableURL else {
            errorMessage = "无法读取这个应用的可执行文件。"
            return
        }
        let bundlePath = canonicalURL.path
        if config.applications.contains(where: { $0.bundlePath.caseInsensitiveCompare(bundlePath) == .orderedSame }) {
            errorMessage = "这个应用已经在规则列表中。"
            return
        }
        if let ownBundlePath = Bundle.main.bundleURL.resolvingSymlinksInPath().path.removingPercentEncoding,
           ownBundlePath.caseInsensitiveCompare(bundlePath) == .orderedSame {
            errorMessage = "DJOneHub 自身不能加入分流规则。"
            return
        }
        let displayName = (bundle.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String)
            ?? (bundle.object(forInfoDictionaryKey: "CFBundleName") as? String)
            ?? canonicalURL.deletingPathExtension().lastPathComponent
        config.applications.append(RoutingApplication(
            id: UUID().uuidString,
            name: displayName,
            bundleID: bundle.bundleIdentifier,
            bundlePath: bundlePath,
            executablePath: executableURL.resolvingSymlinksInPath().path,
            action: .moduleDirect))
    }

    func removeApplication(id: String) {
        config.applications.removeAll { $0.id == id }
    }

    func copyClashConfiguration() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(clashConfigurationYAML, forType: .string)
    }

    private var isLoadFailure: Bool {
        if case .failed = loadPhase { return true }
        return false
    }

    private func schedulePreflight() {
        guard isLoaded, !runtime.enabled else { return }
        preflightDebounceTask?.cancel()
        preflightDebounceTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 350_000_000)
            guard !Task.isCancelled, let self else { return }
            self.preflightDebounceTask = nil
            await self.performPreflight()
        }
    }

    private func performPreflight() async {
        guard isLoaded else { return }
        let revision = configRevision
        let checkedConfig = config
        preflightGeneration &+= 1
        let generation = preflightGeneration
        isChecking = true
        defer {
            if generation == preflightGeneration {
                isChecking = false
            }
        }

        do {
            let result: RoutingPreflight = try await APIClient().send(
                "api/routing/preflight", body: checkedConfig)
            guard generation == preflightGeneration, revision == configRevision else { return }
            preflight = result
            preflightRevision = revision
        } catch {
            guard generation == preflightGeneration, revision == configRevision else { return }
            preflight = nil
            preflightRevision = nil
            errorMessage = error.localizedDescription
        }
    }

    private func invalidatePreflight() {
        preflight = nil
        preflightRevision = nil
        preflightGeneration &+= 1
        isChecking = false
    }

    private func apply(_ snapshot: RoutingSnapshot, includeConfig: Bool) {
        isApplyingSnapshot = true
        if includeConfig {
            if config != snapshot.config {
                config = snapshot.config
                configRevision &+= 1
                invalidatePreflight()
            }
            isDirty = false
        }
        runtime = snapshot.runtime
        capabilities = snapshot.capabilities
        isApplyingSnapshot = false
    }
}

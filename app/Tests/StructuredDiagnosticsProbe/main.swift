import Foundation

@main
struct StructuredDiagnosticsProbe {
    static func main() {
        var buffer = DiagnosticEventBuffer(maxCount: 3)
        let baseTime = Date()
        let initial = fixture(serviceFailed: false, generatedAt: baseTime)
        let first = buffer.record(snapshot: initial)

        expect(!first.isEmpty, "first snapshot must create baseline events")
        expect(buffer.events.allSatisfy { $0.code == .baseline },
               "first snapshot events must be marked as baseline")
        expect(buffer.events.count == 3,
               "bounded buffer must retain only the newest events")

        let unchanged = buffer.record(snapshot: initial)
        expect(unchanged.isEmpty, "unchanged snapshot must not create duplicate events")

        let failed = fixture(serviceFailed: true, generatedAt: baseTime.addingTimeInterval(10))
        let changed = buffer.record(snapshot: failed)
        expect(changed.contains(where: {
            $0.component == .service && $0.code == .stateChanged && $0.level == .critical
        }), "service failure must create a structured transition event")
        expect(buffer.events.count <= 3, "event retention must remain bounded")

        let sensitive = [
            "13800138000",
            "860000000000001",
            "8986000000000000001",
            "460001234567890",
            "session-live-secret",
            "验证码 482913",
            "transcript secret words",
            "/Users/example/Library/Application Support/FlameDJHub/flame-dj-hub.sock",
        ]
        let report = PrivacySafeSupportReport.render(snapshot: failed, events: buffer.events)
        for secret in sensitive {
            expect(!report.contains(secret), "support report leaked sensitive value: \(secret)")
        }
        expect(report.contains("结构化事件"), "support report must include structured events")
        expect(report.contains("后台服务"), "support report must retain useful component names")
        expect(report.contains("故障"), "support report must retain useful severity")

        let hostileSnapshot = RuntimeDiagnosticsSnapshot(
            generatedAt: baseTime,
            overallLevel: .critical,
            items: [
                .init(
                    id: "service",
                    title: sensitive.joined(separator: " "),
                    summary: sensitive.joined(separator: " "),
                    detail: sensitive.joined(separator: " "),
                    level: .critical),
            ])
        let hostileReport = PrivacySafeSupportReport.render(snapshot: hostileSnapshot, events: [])
        let hostileCopy = hostileSnapshot.sanitizedReport
        for secret in sensitive {
            expect(!hostileReport.contains(secret), "support report trusted free text: \(secret)")
            expect(!hostileCopy.contains(secret), "copied diagnostic report trusted free text: \(secret)")
        }

        let encoded = buffer.events.map(\.ndjsonLine).joined(separator: "\n")
        expect(!encoded.contains("summary"), "structured event schema must not contain free-text summary")
        expect(!encoded.contains("detail"), "structured event schema must not contain free-text detail")

        let archive = DiagnosticEventArchive.encode(buffer.events)
        let corrupted = archive + Data("\n{\"summary\":\"13800138000\"}\nnot-json".utf8)
        let decoded = DiagnosticEventArchive.decode(archive, maxCount: 2, now: baseTime.addingTimeInterval(60))
        let expectedSuffix = Array(buffer.events.suffix(2))
        expect(decoded?.map(\.id) == expectedSuffix.map(\.id),
               "archive round trip must preserve validated event identity and order")
        expect(DiagnosticEventArchive.decode(corrupted, maxCount: 2, now: baseTime.addingTimeInterval(60)) == nil,
               "any malformed or free-text archive row must fail closed")

        let validLine = buffer.events[0].ndjsonLine
        let exactLine = Data(
            (String(repeating: " ", count: 4_096 - validLine.utf8.count) + validLine).utf8)
        expect(DiagnosticEventArchive.decode(
            exactLine, maxCount: 200, now: baseTime.addingTimeInterval(60)) != nil,
            "archive rows of exactly 4096 bytes must remain valid")
        let paddedValidLine = Data([0x20]) + exactLine
        expect(DiagnosticEventArchive.decode(
            paddedValidLine, maxCount: 200, now: baseTime.addingTimeInterval(60)) == nil,
            "archive rows exceeding 4096 bytes must fail closed even when JSON is valid")

        var oversizedEvents: [StructuredDiagnosticEvent] = []
        var oversizedLevel = DiagnosticEventLevel.healthy
        var oversizedPrevious: DiagnosticEventLevel?
        var oversizedIndex = 0
        var oversizedArchive = Data()
        repeat {
            let code: DiagnosticEventCode = oversizedPrevious == nil ? .baseline : .stateChanged
            oversizedEvents.append(StructuredDiagnosticEvent(
                timestamp: baseTime.addingTimeInterval(Double(oversizedIndex) / 1_000),
                component: .service,
                code: code,
                level: oversizedLevel,
                previousLevel: oversizedPrevious))
            oversizedPrevious = oversizedLevel
            oversizedLevel = oversizedLevel == .healthy ? .warning : .healthy
            oversizedIndex += 1
            if oversizedIndex.isMultiple(of: 500) {
                oversizedArchive = DiagnosticEventArchive.encode(oversizedEvents)
            }
        } while oversizedArchive.count <= 1_048_576

        var exactArchiveLines = oversizedEvents.dropLast(500).map(\.ndjsonLine)
        var exactArchiveSize = exactArchiveLines.reduce(0) { $0 + $1.utf8.count }
            + max(0, exactArchiveLines.count - 1)
        var remainingPadding = 1_048_576 - exactArchiveSize
        for index in exactArchiveLines.indices where remainingPadding > 0 {
            let capacity = 4_096 - exactArchiveLines[index].utf8.count
            let padding = min(capacity, remainingPadding)
            exactArchiveLines[index] = String(repeating: " ", count: padding) + exactArchiveLines[index]
            remainingPadding -= padding
            exactArchiveSize += padding
        }
        let exactArchive = Data(exactArchiveLines.joined(separator: "\n").utf8)
        expect(remainingPadding == 0 && exactArchive.count == 1_048_576,
               "probe must construct an archive at the exact 1 MiB boundary")
        expect(DiagnosticEventArchive.decode(
            exactArchive, maxCount: 200, now: baseTime.addingTimeInterval(60)) != nil,
            "archives of exactly 1 MiB must remain valid")

        expect(DiagnosticEventArchive.decode(
            oversizedArchive, maxCount: 200, now: baseTime.addingTimeInterval(60)) == nil,
            "archives exceeding 1 MiB must fail closed before unbounded parsing")

        let invalidTransition = StructuredDiagnosticEvent(
            timestamp: baseTime,
            component: .service,
            code: .stateChanged,
            level: .critical,
            previousLevel: .critical)
        expect(DiagnosticEventArchive.decode(
            DiagnosticEventArchive.encode([invalidTransition]),
            maxCount: 200,
            now: baseTime.addingTimeInterval(60)) == nil,
            "state transition with identical levels must fail closed")

        var boundary = DiagnosticEventBuffer(maxCount: 200)
        for index in 0..<201 {
            let level: RuntimeDiagnosticLevel = index.isMultiple(of: 2) ? .healthy : .warning
            let snapshot = RuntimeDiagnosticsSnapshot(
                generatedAt: baseTime.addingTimeInterval(Double(index)),
                overallLevel: level,
                items: [.init(id: "service", title: "ignored", summary: "ignored", detail: "ignored", level: level)])
            _ = boundary.record(snapshot: snapshot)
        }
        expect(boundary.events.count == 200, "201 events must retain exactly the newest 200")
        let retainedArchive = DiagnosticEventArchive.encode(boundary.events)
        let retainedDecoded = DiagnosticEventArchive.decode(
            retainedArchive, maxCount: 200, now: baseTime.addingTimeInterval(300))
        expect(retainedDecoded?.count == 200,
               "retention must not invalidate a legal archive beginning with a transition")

        let oldService = StructuredDiagnosticEvent(
            timestamp: baseTime,
            component: .service,
            code: .baseline,
            level: .healthy,
            previousLevel: nil)
        let retainedModule = StructuredDiagnosticEvent(
            timestamp: baseTime.addingTimeInterval(1),
            component: .module,
            code: .baseline,
            level: .healthy,
            previousLevel: nil)
        var rehydrated = DiagnosticEventBuffer(
            maxCount: 1, events: [oldService, retainedModule])
        let serviceSnapshot = RuntimeDiagnosticsSnapshot(
            generatedAt: baseTime.addingTimeInterval(2),
            overallLevel: .healthy,
            items: [.init(
                id: "service", title: "ignored", summary: "ignored", detail: "ignored",
                level: .healthy)])
        let recreated = rehydrated.record(snapshot: serviceSnapshot)
        expect(recreated.first?.component == .service && recreated.first?.code == .baseline,
               "discarded retention history must not suppress a new component baseline")

        testSecurePersistence(events: Array(boundary.events.suffix(2)), now: baseTime.addingTimeInterval(300))
        testStoreStateConsistency(baseTime: baseTime)
        testNetworkTrafficPolicy()

        print("PASS bounded privacy-safe structured diagnostics")
    }

    private static func testNetworkTrafficPolicy() {
        let unavailable = TrafficSnapshot(
            available: false,
            interface: nil,
            rxBytes: nil,
            txBytes: nil,
            sessionRX: nil,
            sessionTX: nil,
            sessionTotal: nil,
            sampledAtMS: nil,
            error: "未识别到 4G 模块网卡")
        expect(networkTrafficPresentation(unavailable, usbnetEnabled: false) == .cellularDataDisabled,
               "usbnet=0 must be presented as intentionally disabled, not a recovery error")
        expect(networkTrafficPresentation(unavailable, usbnetEnabled: true) == .unavailable,
               "usbnet=1 with no module interface must remain unavailable")

        let available = TrafficSnapshot(
            available: true,
            interface: "en8",
            rxBytes: 1,
            txBytes: 2,
            sessionRX: 1,
            sessionTX: 2,
            sessionTotal: 3,
            sampledAtMS: 4,
            error: nil)
        expect(networkTrafficPresentation(available, usbnetEnabled: false) == .available,
               "a real available sample must remain available")
    }

    @MainActor
    private static func testStoreStateConsistency(baseTime: Date) {
        var archived = Data()
        var failSaveBeforeMutation = true
        var failSaveAfterMutation = false
        var failRemoveBeforeMutation = true
        var failRemoveAfterMutation = false
        let storage = DiagnosticEventStorage(
            load: { archived },
            save: { data in
                if failSaveBeforeMutation { throw CocoaError(.fileWriteUnknown) }
                archived = data
                if failSaveAfterMutation { throw CocoaError(.fileWriteUnknown) }
            },
            remove: {
                if failRemoveBeforeMutation { throw CocoaError(.fileWriteUnknown) }
                archived = Data()
                if failRemoveAfterMutation { throw CocoaError(.fileWriteUnknown) }
            })
        let store = StructuredDiagnosticLogStore(storage: storage, maxCount: 200)
        let initial = fixture(serviceFailed: false, generatedAt: baseTime)

        store.record(snapshot: initial)
        expect(store.events.isEmpty,
               "failed persistence must not publish unarchived events")
        expect(store.storageError != nil,
               "failed persistence must expose a storage error")

        failSaveBeforeMutation = false
        store.record(snapshot: initial)
        expect(!store.events.isEmpty,
               "same snapshot must retry after a failed persistence")
        let persistedIDs = DiagnosticEventArchive.decode(
            archived, maxCount: 200, now: baseTime.addingTimeInterval(60))?.map(\.id)
        expect(persistedIDs == store.events.map(\.id),
               "published events must exactly match the archive")

        let beforePartialSave = store.events
        failSaveAfterMutation = true
        store.record(snapshot: fixture(
            serviceFailed: true, generatedAt: baseTime.addingTimeInterval(10)))
        let archiveAfterPartialSave = DiagnosticEventArchive.decode(
            archived, maxCount: 200, now: baseTime.addingTimeInterval(60)) ?? []
        expect(store.events.map(\.id) == archiveAfterPartialSave.map(\.id),
               "post-mutation save failure must reconcile memory to the archive")
        expect(store.events != beforePartialSave && store.storageError != nil,
               "post-mutation save failure must publish reconciled state and retain the error")
        failSaveAfterMutation = false

        let beforeClear = store.events
        store.clear()
        expect(store.events == beforeClear,
               "failed clear must preserve published events")

        failRemoveBeforeMutation = false
        failRemoveAfterMutation = true
        store.clear()
        expect(store.events.isEmpty && archived.isEmpty && store.storageError != nil,
               "post-mutation clear failure must reconcile memory to the missing archive")

        failRemoveAfterMutation = false
        archived = DiagnosticEventArchive.encode(beforeClear)
        store.load()
        store.clear()
        expect(store.events.isEmpty && archived.isEmpty,
               "successful clear must clear memory and archive")

        archived = DiagnosticEventArchive.encode(beforeClear)
        store.load()
        expect(store.events.map(\.id) == beforeClear.map(\.id),
               "load must replace memory with decoded archive")
        archived = Data()
        store.load()
        expect(store.events.isEmpty,
               "loading an empty archive must clear stale memory")
    }

    @MainActor
    private static func testSecurePersistence(events: [StructuredDiagnosticEvent], now: Date) {
        let root = URL(fileURLWithPath: FileManager.default.currentDirectoryPath, isDirectory: true)
            .appendingPathComponent(".flame-structured-probe-\(UUID().uuidString)")
        let safeDirectory = root.appendingPathComponent("safe")
        let fileName = "events.ndjson"
        defer {
            SecureDiagnosticEventFileTestHooks.afterArchiveSizeValidation = nil
            try? FileManager.default.removeItem(at: root.appendingPathComponent("race-visible"))
            try? FileManager.default.removeItem(at: root.appendingPathComponent("intermediate-link"))
            try? FileManager.default.removeItem(at: root)
        }

        do {
            try SecureDiagnosticEventFile.save(
                DiagnosticEventArchive.encode(events),
                directoryURL: safeDirectory,
                fileName: fileName)
            let loadedData = try SecureDiagnosticEventFile.load(
                directoryURL: safeDirectory,
                fileName: fileName)
            let loaded = DiagnosticEventArchive.decode(loadedData, maxCount: 200, now: now)
            expect(loaded?.map(\.id) == events.map(\.id), "secure event file must round trip")

            do {
                try SecureDiagnosticEventFile.save(
                    Data(repeating: 0x20, count: 1_048_577),
                    directoryURL: safeDirectory,
                    fileName: fileName)
                expect(false, "secure event file must reject oversized writes")
            } catch let error as POSIXError {
                expect(error.code == .EFBIG, "oversized write must report EFBIG")
            } catch {
                expect(false, "oversized write returned unexpected error: \(error)")
            }
            let afterRejectedWrite = try SecureDiagnosticEventFile.load(
                directoryURL: safeDirectory, fileName: fileName)
            expect(afterRejectedWrite == DiagnosticEventArchive.encode(events),
                   "rejected oversized write must preserve the existing archive")

            let attributes = try FileManager.default.attributesOfItem(
                atPath: safeDirectory.appendingPathComponent(fileName).path)
            let permissions = (attributes[.posixPermissions] as? NSNumber)?.intValue
            expect(permissions == 0o600, "event file must be created with mode 0600")

            let oversizedPath = safeDirectory.appendingPathComponent(fileName)
            try Data(repeating: 0x20, count: 1_048_577).write(to: oversizedPath)
            try FileManager.default.setAttributes(
                [.posixPermissions: 0o600], ofItemAtPath: oversizedPath.path)
            do {
                _ = try SecureDiagnosticEventFile.load(
                    directoryURL: safeDirectory, fileName: fileName)
                expect(false, "secure event file must reject archives exceeding 1 MiB")
            } catch let error as CocoaError {
                expect(error.code == .fileReadTooLarge,
                       "oversized archive must report fileReadTooLarge")
            } catch {
                expect(false, "oversized archive returned unexpected error: \(error)")
            }

            try SecureDiagnosticEventFile.save(
                DiagnosticEventArchive.encode(events),
                directoryURL: safeDirectory,
                fileName: fileName)
            SecureDiagnosticEventFileTestHooks.afterArchiveSizeValidation = {
                SecureDiagnosticEventFileTestHooks.afterArchiveSizeValidation = nil
                let handle = try FileHandle(forWritingTo: oversizedPath)
                defer { try? handle.close() }
                try handle.seekToEnd()
                try handle.write(contentsOf: Data(repeating: 0x20, count: 1_048_577))
            }
            do {
                _ = try SecureDiagnosticEventFile.load(
                    directoryURL: safeDirectory, fileName: fileName)
                expect(false, "archive growth after fstat must be rejected")
            } catch let error as CocoaError {
                expect(error.code == .fileReadTooLarge,
                       "archive growth race must report fileReadTooLarge")
            } catch {
                expect(false, "archive growth race returned unexpected error: \(error)")
            }
            SecureDiagnosticEventFileTestHooks.afterArchiveSizeValidation = nil

            try SecureDiagnosticEventFile.save(
                DiagnosticEventArchive.encode(events),
                directoryURL: safeDirectory,
                fileName: fileName)
            try FileManager.default.setAttributes(
                [.posixPermissions: 0o644],
                ofItemAtPath: safeDirectory.appendingPathComponent(fileName).path)
            do {
                _ = try SecureDiagnosticEventFile.load(
                    directoryURL: safeDirectory, fileName: fileName)
                expect(false, "event file with permissive mode must fail closed")
            } catch {}
        } catch {
            expect(false, "secure event file unexpectedly failed: \(error)")
        }

        let symlinkTarget = root.appendingPathComponent("target.txt")
        let symlinkFile = safeDirectory.appendingPathComponent(fileName)
        do {
            try Data("do-not-touch".utf8).write(to: symlinkTarget)
            try FileManager.default.removeItem(at: symlinkFile)
            try FileManager.default.createSymbolicLink(at: symlinkFile, withDestinationURL: symlinkTarget)
            do {
                try SecureDiagnosticEventFile.save(
                    Data("replacement".utf8), directoryURL: safeDirectory, fileName: fileName)
                expect(false, "symlink destination must be rejected")
            } catch {}
            let target = try String(contentsOf: symlinkTarget, encoding: .utf8)
            expect(target == "do-not-touch", "symlink target must remain untouched")
        } catch {
            expect(false, "symlink file probe setup failed: \(error)")
        }

        testDirectoryReplacementRace(root: root, fileName: fileName)
        testIntermediateSymlinkRejection(root: root, fileName: fileName)

        let realDirectory = root.appendingPathComponent("real-directory")
        let linkedDirectory = root.appendingPathComponent("linked-directory")
        do {
            try FileManager.default.createDirectory(at: realDirectory, withIntermediateDirectories: true)
            try FileManager.default.createSymbolicLink(at: linkedDirectory, withDestinationURL: realDirectory)
            do {
                try SecureDiagnosticEventFile.save(
                    Data("blocked".utf8), directoryURL: linkedDirectory, fileName: fileName)
                expect(false, "symlink support directory must be rejected")
            } catch {}
        } catch {
            expect(false, "symlink directory probe setup failed: \(error)")
        }
    }

    private static func testDirectoryReplacementRace(root: URL, fileName: String) {
        let visible = root.appendingPathComponent("race-visible")
        let opened = root.appendingPathComponent("race-opened")
        let attacker = root.appendingPathComponent("race-attacker")
        do {
            try FileManager.default.createDirectory(at: visible, withIntermediateDirectories: true)
            try FileManager.default.createDirectory(at: attacker, withIntermediateDirectories: true)
            SecureDiagnosticEventFileTestHooks.afterDirectoryOpen = {
                SecureDiagnosticEventFileTestHooks.afterDirectoryOpen = nil
                try FileManager.default.moveItem(at: visible, to: opened)
                try FileManager.default.createSymbolicLink(at: visible, withDestinationURL: attacker)
            }
            try SecureDiagnosticEventFile.save(
                Data("fd-bound".utf8), directoryURL: visible, fileName: fileName)
            expect(!FileManager.default.fileExists(
                atPath: attacker.appendingPathComponent(fileName).path),
                "directory replacement must not redirect an atomic save")
            let stored = try String(
                contentsOf: opened.appendingPathComponent(fileName), encoding: .utf8)
            expect(stored == "fd-bound",
                   "atomic save must remain bound to the opened directory")
        } catch {
            expect(false, "directory replacement race probe failed: \(error)")
        }
        SecureDiagnosticEventFileTestHooks.afterDirectoryOpen = nil
    }

    @MainActor
    private static func testIntermediateSymlinkRejection(root: URL, fileName: String) {
        let realParent = root.appendingPathComponent("intermediate-real")
        let linkedParent = root.appendingPathComponent("intermediate-link")
        let nested = linkedParent.appendingPathComponent("nested")
        do {
            try FileManager.default.createDirectory(
                at: realParent.appendingPathComponent("nested"),
                withIntermediateDirectories: true)
            try FileManager.default.createSymbolicLink(
                at: linkedParent, withDestinationURL: realParent)
            do {
                try SecureDiagnosticEventFile.save(
                    Data("blocked".utf8), directoryURL: nested, fileName: fileName)
                expect(false, "intermediate directory symlink must be rejected")
            } catch {}
            expect(!FileManager.default.fileExists(
                atPath: realParent.appendingPathComponent("nested").appendingPathComponent(fileName).path),
                "intermediate symlink target must remain untouched")

            let store = StructuredDiagnosticLogStore(
                fileURL: nested.appendingPathComponent(fileName), maxCount: 200)
            expect(store.storageError != nil && store.events.isEmpty,
                   "production file-backed store must reject an intermediate symlink on load")
            store.record(snapshot: fixture(serviceFailed: false, generatedAt: Date()))
            expect(store.storageError != nil && store.events.isEmpty,
                   "production file-backed store must reject an intermediate symlink on save")
            expect(!FileManager.default.fileExists(
                atPath: realParent.appendingPathComponent("nested").appendingPathComponent(fileName).path),
                "file-backed store must not touch an intermediate symlink target")
        } catch {
            expect(false, "intermediate symlink probe setup failed: \(error)")
        }
    }

    private static func fixture(serviceFailed: Bool, generatedAt: Date) -> RuntimeDiagnosticsSnapshot {
        RuntimeDiagnosticsSnapshot.build(
            serviceRunning: true,
            serviceFailed: serviceFailed,
            statusStale: false,
            moduleConnected: true,
            callModeState: "ready",
            callModeSummary: "13800138000 IMEI 860000000000001 session-live-secret 验证码 482913 transcript secret words /Users/example/private.sock",
            callStatus: CallStatus(
                state: "idle", number: nil, incoming: false, active: false,
                sessionID: nil, dtmfAvailable: false, postDialState: nil, postDialError: nil),
            verification: .inactive,
            agentAudio: nil,
            agentStatusReadFailed: false,
            generatedAt: generatedAt)
    }

    private static func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
        guard condition() else {
            fputs("FAIL: \(message)\n", stderr)
            exit(1)
        }
    }
}

import Combine
import Foundation

struct DiagnosticEventStorage {
    let load: () throws -> Data
    let save: (Data) throws -> Void
    let remove: () throws -> Void

    static func secureFile(directoryURL: URL, fileName: String) -> DiagnosticEventStorage {
        DiagnosticEventStorage(
            load: {
                try SecureDiagnosticEventFile.load(
                    directoryURL: directoryURL,
                    fileName: fileName)
            },
            save: { data in
                try SecureDiagnosticEventFile.save(
                    data,
                    directoryURL: directoryURL,
                    fileName: fileName)
            },
            remove: {
                try SecureDiagnosticEventFile.remove(
                    directoryURL: directoryURL,
                    fileName: fileName)
            })
    }
}

@MainActor
final class StructuredDiagnosticLogStore: ObservableObject {
    @Published private(set) var events: [StructuredDiagnosticEvent]
    @Published private(set) var storageError: String?

    private let storage: DiagnosticEventStorage
    private let maxCount: Int
    private var buffer: DiagnosticEventBuffer

    init(fileURL: URL, maxCount: Int = 200) {
        self.storage = .secureFile(
            directoryURL: fileURL.deletingLastPathComponent(),
            fileName: fileURL.lastPathComponent)
        self.maxCount = max(1, maxCount)
        self.events = []
        self.buffer = DiagnosticEventBuffer(maxCount: self.maxCount)
        load()
    }

    init(storage: DiagnosticEventStorage, maxCount: Int = 200) {
        self.storage = storage
        self.maxCount = max(1, maxCount)
        self.events = []
        self.buffer = DiagnosticEventBuffer(maxCount: self.maxCount)
        load()
    }

    #if !STRUCTURED_DIAGNOSTICS_PROBE
    static func live() -> StructuredDiagnosticLogStore {
        let directory = FlameRuntimePaths.supportDirectory
        return StructuredDiagnosticLogStore(
            fileURL: directory.appendingPathComponent("diagnostic-events.ndjson"),
            maxCount: 200)
    }
    #endif

    func load() {
        do {
            let data = try storage.load()
            let decoded: [StructuredDiagnosticEvent]
            if data.isEmpty {
                decoded = []
            } else {
                guard let archive = DiagnosticEventArchive.decode(data, maxCount: maxCount) else {
                    throw CocoaError(.fileReadCorruptFile)
                }
                decoded = archive
            }
            buffer = DiagnosticEventBuffer(maxCount: maxCount, events: decoded)
            events = buffer.events
            storageError = nil
        } catch {
            buffer = DiagnosticEventBuffer(maxCount: maxCount)
            events = []
            storageError = "结构化事件归档不可用，已拒绝加载"
        }
    }

    func record(snapshot: RuntimeDiagnosticsSnapshot) {
        var candidate = buffer
        let created = candidate.record(snapshot: snapshot, observedAt: Date())
        guard !created.isEmpty else { return }
        do {
            try storage.save(DiagnosticEventArchive.encode(candidate.events))
            buffer = candidate
            events = candidate.events
            storageError = nil
        } catch {
            reconcileAfterMutationFailure(message: "保存结构化事件失败")
        }
    }

    func clear() {
        do {
            try storage.remove()
            buffer = DiagnosticEventBuffer(maxCount: maxCount)
            events = []
            storageError = nil
        } catch {
            reconcileAfterMutationFailure(message: "清空结构化事件失败")
        }
    }

    private func reconcileAfterMutationFailure(message: String) {
        do {
            let data = try storage.load()
            let decoded: [StructuredDiagnosticEvent]
            if data.isEmpty {
                decoded = []
            } else {
                guard let archive = DiagnosticEventArchive.decode(data, maxCount: maxCount) else {
                    throw CocoaError(.fileReadCorruptFile)
                }
                decoded = archive
            }
            buffer = DiagnosticEventBuffer(maxCount: maxCount, events: decoded)
            events = buffer.events
        } catch {
            buffer = DiagnosticEventBuffer(maxCount: maxCount)
            events = []
        }
        storageError = message
    }
}

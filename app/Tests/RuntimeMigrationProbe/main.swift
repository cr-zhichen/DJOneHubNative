import Foundation

private enum ProbeFailure: Error, CustomStringConvertible {
    case assertion(String)

    var description: String {
        switch self {
        case .assertion(let message): return message
        }
    }
}

private func require(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    if !condition() {
        throw ProbeFailure.assertion(message)
    }
}

private func write(_ text: String, to url: URL, fileManager: FileManager) throws {
    try fileManager.createDirectory(
        at: url.deletingLastPathComponent(),
        withIntermediateDirectories: true
    )
    try Data(text.utf8).write(to: url)
}

@main
struct RuntimeMigrationProbe {
    static func main() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("flame-runtime-migration-\(UUID().uuidString)", isDirectory: true)
        defer { try? fileManager.removeItem(at: root) }

        let legacy = root.appendingPathComponent("DJOneHubNative", isDirectory: true)
        let destination = root.appendingPathComponent("FlameDJHub", isDirectory: true)
        try fileManager.createDirectory(at: legacy, withIntermediateDirectories: true)
        try fileManager.createDirectory(at: destination, withIntermediateDirectories: true)

        try write("archive", to: legacy.appendingPathComponent("archive/messages.json"), fileManager: fileManager)
        try write("hidden", to: legacy.appendingPathComponent(".state/settings.json"), fileManager: fileManager)
        for name in ["runtime.sock", "backend.LOG", "worker.pid", "partial.tmp"] {
            try write("transient", to: legacy.appendingPathComponent("nested/deeper/\(name)"), fileManager: fileManager)
        }
        try write("legacy-new", to: legacy.appendingPathComponent("existing.json"), fileManager: fileManager)
        try write("destination-old", to: destination.appendingPathComponent("existing.json"), fileManager: fileManager)

        let linkedFile = legacy.appendingPathComponent("linked.json")
        try fileManager.createSymbolicLink(
            at: linkedFile,
            withDestinationURL: legacy.appendingPathComponent("archive/messages.json")
        )
        let linkedDirectory = legacy.appendingPathComponent("linked-directory")
        try fileManager.createSymbolicLink(
            at: linkedDirectory,
            withDestinationURL: legacy.appendingPathComponent("archive", isDirectory: true)
        )

        try FlameRuntimePaths.migrateSupportData(
            from: legacy,
            to: destination,
            fileManager: fileManager
        )

        let migratedArchive = try Data(contentsOf: destination.appendingPathComponent("archive/messages.json"))
        try require(
            String(decoding: migratedArchive, as: UTF8.self) == "archive",
            "nested persistent data was not copied"
        )
        let migratedHiddenState = try Data(contentsOf: destination.appendingPathComponent(".state/settings.json"))
        try require(
            String(decoding: migratedHiddenState, as: UTF8.self) == "hidden",
            "hidden persistent data was not copied"
        )
        let preservedDestination = try Data(contentsOf: destination.appendingPathComponent("existing.json"))
        try require(
            String(decoding: preservedDestination, as: UTF8.self) == "destination-old",
            "existing destination data was overwritten"
        )
        for name in ["runtime.sock", "backend.LOG", "worker.pid", "partial.tmp"] {
            try require(
                !fileManager.fileExists(atPath: destination.appendingPathComponent("nested/deeper/\(name)").path),
                "transient file migrated: \(name)"
            )
        }
        try require(!fileManager.fileExists(atPath: destination.appendingPathComponent("linked.json").path), "file symlink migrated")
        try require(!fileManager.fileExists(atPath: destination.appendingPathComponent("linked-directory").path), "directory symlink migrated")
        try require(fileManager.fileExists(atPath: legacy.appendingPathComponent("archive/messages.json").path), "legacy data was deleted")

        let destinationEntries = try fileManager.subpathsOfDirectory(atPath: destination.path)
        try require(
            destinationEntries.allSatisfy { !$0.contains(".migration-") },
            "migration temporary file was left behind"
        )

        let failingSource = root.appendingPathComponent("failing-source", isDirectory: true)
        let failingDestination = root.appendingPathComponent("failing-destination", isDirectory: true)
        let unreadableSource = failingSource.appendingPathComponent("unreadable.json")
        try write("unreadable", to: unreadableSource, fileManager: fileManager)
        try fileManager.setAttributes([.posixPermissions: 0o000], ofItemAtPath: unreadableSource.path)
        defer { try? fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: unreadableSource.path) }
        do {
            try FlameRuntimePaths.migrateSupportData(
                from: failingSource,
                to: failingDestination,
                fileManager: fileManager
            )
            throw ProbeFailure.assertion("unreadable source migration did not fail")
        } catch let error as ProbeFailure {
            throw error
        } catch {
            let failedEntries = try fileManager.contentsOfDirectory(atPath: failingDestination.path)
            try require(
                failedEntries.allSatisfy { !$0.hasPrefix(".migration-") },
                "failed migration left a temporary file"
            )
            try require(
                !fileManager.fileExists(atPath: failingDestination.appendingPathComponent("unreadable.json").path),
                "failed migration published a partial destination"
            )
        }

        let invalidDestination = root.appendingPathComponent("destination-file")
        try write("not-a-directory", to: invalidDestination, fileManager: fileManager)
        do {
            try FlameRuntimePaths.migrateSupportData(
                from: legacy,
                to: invalidDestination,
                fileManager: fileManager
            )
            throw ProbeFailure.assertion("migration failure was silently ignored")
        } catch let error as ProbeFailure {
            throw error
        } catch {
            // Expected: callers can record the error and continue with the destination path.
        }

        print("runtime migration probe passed")
    }
}

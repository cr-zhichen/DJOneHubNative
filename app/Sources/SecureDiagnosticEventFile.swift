import Foundation

#if STRUCTURED_DIAGNOSTICS_PROBE
enum SecureDiagnosticEventFileTestHooks {
    static var afterDirectoryOpen: (() throws -> Void)?
    static var afterArchiveSizeValidation: (() throws -> Void)?
}
#endif

/// File-level boundary for the local structured diagnostic archive.
/// Every archive operation stays relative to an opened, no-symlink directory descriptor.
enum SecureDiagnosticEventFile {
    static func load(directoryURL: URL, fileName: String) throws -> Data {
        try validateFileName(fileName)
        let directory = try openDirectory(directoryURL, createIfMissing: false)
        defer { close(directory) }
        try runAfterDirectoryOpenHook()

        let descriptor = openat(directory, fileName, O_RDONLY | O_NOFOLLOW)
        if descriptor < 0, errno == ENOENT { return Data() }
        guard descriptor >= 0 else { throw currentPOSIXError() }
        defer { close(descriptor) }
        try validateRegularFile(descriptor)
        try validateArchiveSize(descriptor)
        try runAfterArchiveSizeValidationHook()

        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 16_384)
        while true {
            let count = read(descriptor, &buffer, buffer.count)
            if count == 0 { break }
            if count < 0 {
                if errno == EINTR { continue }
                throw currentPOSIXError()
            }
            guard data.count <= DiagnosticEventArchive.maxArchiveBytes - count else {
                throw CocoaError(.fileReadTooLarge)
            }
            data.append(buffer, count: count)
        }
        return data
    }

    static func save(_ data: Data, directoryURL: URL, fileName: String) throws {
        try validateFileName(fileName)
        guard data.count <= DiagnosticEventArchive.maxArchiveBytes else {
            throw POSIXError(.EFBIG)
        }
        let directory = try openDirectory(directoryURL, createIfMissing: true)
        defer { close(directory) }
        try runAfterDirectoryOpenHook()
        try validateExistingArchiveIfPresent(directory: directory, fileName: fileName)

        let temporaryName = ".\(fileName).\(UUID().uuidString).tmp"
        let descriptor = openat(
            directory,
            temporaryName,
            O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW,
            0o600)
        guard descriptor >= 0 else { throw currentPOSIXError() }
        var temporaryExists = true
        defer {
            close(descriptor)
            if temporaryExists { _ = unlinkat(directory, temporaryName, 0) }
        }

        try data.withUnsafeBytes { bytes in
            var offset = 0
            while offset < bytes.count {
                guard let baseAddress = bytes.baseAddress else { break }
                let written = write(
                    descriptor,
                    baseAddress.advanced(by: offset),
                    bytes.count - offset)
                if written < 0, errno == EINTR { continue }
                guard written > 0 else { throw currentPOSIXError() }
                offset += written
            }
        }
        guard fchmod(descriptor, 0o600) == 0 else { throw currentPOSIXError() }
        try validateRegularFile(descriptor)
        guard fsync(descriptor) == 0 else { throw currentPOSIXError() }
        guard renameat(directory, temporaryName, directory, fileName) == 0 else {
            throw currentPOSIXError()
        }
        temporaryExists = false
        _ = fsync(directory)
    }

    static func remove(directoryURL: URL, fileName: String) throws {
        try validateFileName(fileName)
        let directory: Int32
        do {
            directory = try openDirectory(directoryURL, createIfMissing: false)
        } catch let error as POSIXError where error.code == .ENOENT {
            return
        }
        defer { close(directory) }
        try runAfterDirectoryOpenHook()

        do {
            try validateExistingArchive(directory: directory, fileName: fileName)
        } catch let error as POSIXError where error.code == .ENOENT {
            return
        }
        guard unlinkat(directory, fileName, 0) == 0 else { throw currentPOSIXError() }
        _ = fsync(directory)
    }

    private static func openDirectory(_ url: URL, createIfMissing: Bool) throws -> Int32 {
        guard url.isFileURL, url.path.hasPrefix("/") else {
            throw CocoaError(.fileReadInvalidFileName)
        }
        var current = open("/", O_RDONLY | O_DIRECTORY | O_NOFOLLOW)
        guard current >= 0 else { throw currentPOSIXError() }

        let components = url.standardizedFileURL.pathComponents.filter { $0 != "/" }
        do {
            for component in components {
                try validatePathComponent(component)
                var next = openat(current, component, O_RDONLY | O_DIRECTORY | O_NOFOLLOW)
                if next < 0, errno == ENOENT, createIfMissing {
                    guard mkdirat(current, component, 0o700) == 0 || errno == EEXIST else {
                        throw currentPOSIXError()
                    }
                    next = openat(current, component, O_RDONLY | O_DIRECTORY | O_NOFOLLOW)
                }
                guard next >= 0 else { throw currentPOSIXError() }
                close(current)
                current = next
            }
            try validateDirectory(current)
            return current
        } catch {
            close(current)
            throw error
        }
    }

    private static func validateExistingArchiveIfPresent(directory: Int32, fileName: String) throws {
        do {
            try validateExistingArchive(directory: directory, fileName: fileName)
        } catch let error as POSIXError where error.code == .ENOENT {
            return
        }
    }

    private static func validateExistingArchive(directory: Int32, fileName: String) throws {
        var info = stat()
        guard fstatat(directory, fileName, &info, AT_SYMLINK_NOFOLLOW) == 0 else {
            throw currentPOSIXError()
        }
        guard (info.st_mode & S_IFMT) == S_IFREG,
              info.st_uid == getuid(),
              (info.st_mode & mode_t(0o777)) == mode_t(0o600) else {
            throw CocoaError(.fileReadNoPermission)
        }
    }

    private static func validateDirectory(_ descriptor: Int32) throws {
        var info = stat()
        guard fstat(descriptor, &info) == 0 else { throw currentPOSIXError() }
        guard (info.st_mode & S_IFMT) == S_IFDIR, info.st_uid == getuid() else {
            throw CocoaError(.fileReadNoPermission)
        }
    }

    private static func validateRegularFile(_ descriptor: Int32) throws {
        var info = stat()
        guard fstat(descriptor, &info) == 0 else { throw currentPOSIXError() }
        guard (info.st_mode & S_IFMT) == S_IFREG,
              info.st_uid == getuid(),
              (info.st_mode & mode_t(0o777)) == mode_t(0o600) else {
            throw CocoaError(.fileReadNoPermission)
        }
    }

    private static func validateArchiveSize(_ descriptor: Int32) throws {
        var info = stat()
        guard fstat(descriptor, &info) == 0 else { throw currentPOSIXError() }
        guard info.st_size >= 0,
              info.st_size <= off_t(DiagnosticEventArchive.maxArchiveBytes) else {
            throw CocoaError(.fileReadTooLarge)
        }
    }

    private static func validateFileName(_ fileName: String) throws {
        try validatePathComponent(fileName)
        guard !fileName.contains("/") else { throw CocoaError(.fileReadInvalidFileName) }
    }

    private static func validatePathComponent(_ component: String) throws {
        guard !component.isEmpty, component != ".", component != "..", !component.contains("/") else {
            throw CocoaError(.fileReadInvalidFileName)
        }
    }

    private static func currentPOSIXError() -> POSIXError {
        POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
    }

    private static func runAfterArchiveSizeValidationHook() throws {
        #if STRUCTURED_DIAGNOSTICS_PROBE
        try SecureDiagnosticEventFileTestHooks.afterArchiveSizeValidation?()
        #endif
    }

    private static func runAfterDirectoryOpenHook() throws {
        #if STRUCTURED_DIAGNOSTICS_PROBE
        try SecureDiagnosticEventFileTestHooks.afterDirectoryOpen?()
        #endif
    }
}

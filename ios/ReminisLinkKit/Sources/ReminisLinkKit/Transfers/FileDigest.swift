import CryptoKit
import Foundation

/// File helpers of the uploads that never load a whole file in memory: the
/// file is read through a fixed buffer (1 MiB).
public enum FileDigest {
    public static let bufferSize = 1 << 20

    /// Lowercase hex SHA-256 of a file, computed while reading it.
    /// `progress` gets the number of bytes hashed so far.
    /// `shouldPause` is checked between buffers; true stops with
    /// ``UploadFailure/paused``.
    @concurrent
    public static func sha256(
        of url: URL,
        shouldPause: (@Sendable () -> Bool)? = nil,
        progress: (@Sendable (Int64) -> Void)? = nil
    ) async throws -> String {
        try sha256Sync(of: url, shouldPause: shouldPause, progress: progress)
    }

    /// Synchronous version (checks the task cancellation between buffers).
    public static func sha256Sync(
        of url: URL,
        bufferSize: Int = FileDigest.bufferSize,
        shouldPause: (@Sendable () -> Bool)? = nil,
        progress: (@Sendable (Int64) -> Void)? = nil
    ) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        var total: Int64 = 0
        while true {
            if Task.isCancelled { throw CancellationError() }
            if shouldPause?() == true { throw UploadFailure.paused }
            let read = try autoreleasepool { () throws -> Int in
                guard let data = try handle.read(upToCount: bufferSize), !data.isEmpty else { return 0 }
                hasher.update(data: data)
                return data.count
            }
            if read == 0 { break }
            total += Int64(read)
            progress?(total)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    /// Size of a regular file, read from the file system each time (the
    /// resource values of a `URL` are cached by the URL object).
    public static func size(of url: URL) throws -> Int64 {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path(percentEncoded: false))
        guard attributes[.type] as? FileAttributeType == .typeRegular,
              let size = (attributes[.size] as? NSNumber)?.int64Value
        else {
            throw CocoaError(.fileReadUnknown)
        }
        return size
    }

    /// Copies `length` bytes of `source` from `offset` into a new file at
    /// `destination` (replaced if present), through the fixed buffer. This
    /// is the body of one upload chunk, streamed by `URLSession` from disk.
    public static func writeChunk(of source: URL, offset: Int64, length: Int64, to destination: URL) throws {
        let input = try FileHandle(forReadingFrom: source)
        defer { try? input.close() }
        try input.seek(toOffset: UInt64(offset))
        let manager = FileManager.default
        try? manager.removeItem(at: destination)
        guard manager.createFile(atPath: destination.path, contents: nil) else {
            throw CocoaError(.fileWriteUnknown)
        }
        let output = try FileHandle(forWritingTo: destination)
        defer { try? output.close() }
        var remaining = length
        while remaining > 0 {
            if Task.isCancelled { throw CancellationError() }
            let read = try autoreleasepool { () throws -> Int in
                guard let data = try input.read(upToCount: Int(min(Int64(bufferSize), remaining))), !data.isEmpty else {
                    return 0
                }
                try output.write(contentsOf: data)
                return data.count
            }
            // The file got shorter than announced.
            guard read > 0 else { throw UploadFailure.fileChanged }
            remaining -= Int64(read)
        }
    }
}

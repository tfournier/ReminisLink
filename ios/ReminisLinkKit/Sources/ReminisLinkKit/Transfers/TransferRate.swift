import Foundation

/// Speed and time left of a transfer, over a sliding window.
public struct TransferRate: Sendable, Equatable {
    public let window: TimeInterval
    private var samples: [Sample] = []

    private struct Sample: Sendable, Equatable {
        let time: TimeInterval
        let bytes: Int64
    }

    public init(window: TimeInterval = 5) {
        self.window = window
    }

    /// Records the bytes done at a time (seconds, any monotonic origin).
    /// Bytes going back (an upload started again) reset the measure.
    public mutating func record(bytes: Int64, at time: TimeInterval) {
        if let last = samples.last, bytes < last.bytes { samples.removeAll() }
        samples.append(Sample(time: time, bytes: bytes))
        while let first = samples.first, samples.count > 2, time - first.time > window {
            samples.removeFirst()
        }
    }

    public mutating func reset() {
        samples.removeAll()
    }

    /// Bytes per second, nil until two samples at least 0.5 s apart.
    public var bytesPerSecond: Double? {
        guard let first = samples.first, let last = samples.last, last.time - first.time >= 0.5 else { return nil }
        return Double(last.bytes - first.bytes) / (last.time - first.time)
    }

    /// Seconds left to reach `total`, nil without a speed.
    public func secondsRemaining(total: Int64) -> TimeInterval? {
        guard let speed = bytesPerSecond, speed > 0, let last = samples.last else { return nil }
        return Double(max(0, total - last.bytes)) / speed
    }
}

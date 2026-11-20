import Foundation

/// Work that writes files of the app (copies of files chosen in Files,
/// downloads, patch uploads), counted so that the end of the demo mode
/// (M16) can cancel it and wait for it before erasing its folder. Once
/// closed, new work is refused.
@MainActor
final class WorkTracker {
    private var running = 0
    private var idleWaiters: [CheckedContinuation<Void, Never>] = []
    /// Cancels the task running each piece of work.
    private var cancellers: [UUID: () -> Void] = [:]
    private(set) var isClosed = false

    /// Runs `body`, counted; `CancellationError` once closed. ``close()``
    /// cancels the task it runs in.
    func run<T>(_ body: () async throws -> T) async throws -> T {
        guard !isClosed else { throw CancellationError() }
        let id = UUID()
        running += 1
        // Used only while this task runs (removed below, before it ends).
        withUnsafeCurrentTask { task in
            if let task { cancellers[id] = { task.cancel() } }
        }
        defer {
            cancellers[id] = nil
            finishOne()
        }
        return try await body()
    }

    /// Refuses new work, cancels the running work and waits for it to end
    /// (work that ignores the cancellation is waited for: the caller bounds
    /// the wait).
    func close() async {
        isClosed = true
        for cancel in cancellers.values { cancel() }
        guard running > 0 else { return }
        await withCheckedContinuation { idleWaiters.append($0) }
    }

    private func finishOne() {
        running -= 1
        guard running == 0 else { return }
        let waiters = idleWaiters
        idleWaiters = []
        for waiter in waiters { waiter.resume() }
    }
}

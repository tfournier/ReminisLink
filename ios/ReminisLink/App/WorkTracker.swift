import Foundation

/// Work that writes files of the app (copies of files chosen in Files,
/// downloads), counted so that the end of the demo mode (M16) can wait for
/// it before erasing its folder. Once closed, new work is refused.
@MainActor
final class WorkTracker {
    private var running = 0
    private var idleWaiters: [CheckedContinuation<Void, Never>] = []
    private(set) var isClosed = false

    /// Runs `body`, counted; `CancellationError` once closed.
    func run<T>(_ body: () async throws -> T) async throws -> T {
        guard !isClosed else { throw CancellationError() }
        running += 1
        defer { finishOne() }
        return try await body()
    }

    /// Refuses new work and waits for the running work to end.
    func close() async {
        isClosed = true
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

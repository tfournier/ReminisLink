/// Overlapping loads of one screen (a refresh, a `library` event, M14):
/// only the last one started may show its answer, whatever order the
/// answers come back in.
struct LoadGeneration {
    private(set) var current = 0

    /// A load starts: its generation.
    mutating func begin() -> Int {
        current += 1
        return current
    }

    /// Whether a load is still the last one started.
    func isCurrent(_ generation: Int) -> Bool {
        generation == current
    }
}

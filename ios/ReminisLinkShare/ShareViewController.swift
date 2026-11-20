import ReminisLinkKit
import UIKit
import UniformTypeIdentifiers

/// Share extension (docs/specs/06-app-ios.md §4.8). It copies the shared
/// files into the app group (`ShareInbox`), then opens the app on the Add
/// screen, prefilled. Web links are not accepted: the console never
/// downloads a link (removed from v1). It never talks to a console: uploads need
/// more memory and time than an extension gets. Files are copied by the
/// file system, never loaded in memory.
final class ShareViewController: UIViewController {
    private let spinner = UIActivityIndicatorView(style: .large)
    private let label = UILabel()
    private let button = UIButton(configuration: .filled())
    private var started = false

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground
        label.font = .preferredFont(forTextStyle: .body)
        label.adjustsFontForContentSizeCategory = true
        label.numberOfLines = 0
        label.textAlignment = .center
        label.text = String(localized: "Preparing…")
        button.configuration?.title = String(localized: "Done")
        button.isHidden = true
        button.addAction(UIAction { [weak self] _ in self?.finish() }, for: .primaryActionTriggered)
        spinner.startAnimating()
        let stack = UIStackView(arrangedSubviews: [spinner, label, button])
        stack.axis = .vertical
        stack.spacing = 16
        stack.alignment = .center
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerYAnchor.constraint(equalTo: view.centerYAnchor),
            stack.leadingAnchor.constraint(equalTo: view.layoutMarginsGuide.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: view.layoutMarginsGuide.trailingAnchor),
        ])
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        guard !started else { return }
        started = true
        Task { await run() }
    }

    private func run() async {
        guard let group = Bundle.main.object(forInfoDictionaryKey: "RLAppGroup") as? String,
              let inbox = ShareInbox.appGroup(group)
        else {
            show(String(localized: "ReminisLink cannot receive files on this device."))
            return
        }
        let providers = (extensionContext?.inputItems as? [NSExtensionItem] ?? []).flatMap { $0.attachments ?? [] }
        let scratch = FileManager.default.temporaryDirectory.appending(path: "share-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: scratch) }
        do {
            try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
            var files: [URL] = []
            for provider in providers {
                if let url = try await Self.load(provider, into: scratch) { files.append(url) }
            }
            guard !files.isEmpty else {
                show(String(localized: "Nothing to add: share files."))
                return
            }
            let request = try inbox.write(files: files)
            if openApp(ShareInbox.url(for: request)) {
                finish()
            } else {
                show(String(localized: "Saved. Open ReminisLink to send it to your console."))
            }
        } catch {
            show(String(localized: "The shared items could not be copied."))
        }
    }

    private func show(_ message: String) {
        spinner.stopAnimating()
        spinner.isHidden = true
        label.text = message
        button.isHidden = false
        UIAccessibility.post(notification: .screenChanged, argument: label)
    }

    private func finish() {
        extensionContext?.completeRequest(returningItems: nil)
    }

    /// Opens the containing app. Share extensions have no public API for
    /// it: the call goes through the responder chain to the application
    /// (`openURL:options:completionHandler:`). If it is not found, the user
    /// is asked to open the app, which picks the request up when it
    /// becomes active.
    private func openApp(_ url: URL) -> Bool {
        let selector = NSSelectorFromString("openURL:options:completionHandler:")
        var responder: UIResponder? = self
        while let current = responder {
            if current !== self, current.responds(to: selector), String(describing: type(of: current)).contains("Application") {
                typealias OpenURL = @convention(c) (AnyObject, Selector, NSURL, NSDictionary, AnyObject?) -> Void
                let function = unsafeBitCast(current.method(for: selector), to: OpenURL.self)
                function(current, selector, url as NSURL, NSDictionary(), nil)
                return true
            }
            responder = current.next
        }
        return false
    }

    /// A file, copied into `directory` while the provider's copy exists. A
    /// web link (no data) is skipped.
    nonisolated static func load(_ provider: NSItemProvider, into directory: URL) async throws -> URL? {
        let types = provider.registeredTypeIdentifiers.compactMap { UTType($0) }
        let isWebLink = types.contains { $0.conforms(to: .url) && !$0.conforms(to: .fileURL) }
            && !types.contains { $0.conforms(to: .data) }
        if isWebLink { return nil }
        guard let type = types.first(where: { $0.conforms(to: .data) }) ?? types.first else { return nil }
        let name = provider.suggestedName
        let copied: URL? = try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadFileRepresentation(forTypeIdentifier: type.identifier) { url, error in
                // The provider deletes its file when this closure returns.
                do {
                    guard let url else { throw error ?? CocoaError(.fileReadUnknown) }
                    continuation.resume(returning: try copy(url, name: name, into: directory))
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
        return copied
    }

    /// Copies a file under its original name (the suggested name keeps the
    /// extension of the file when it has none).
    nonisolated static func copy(_ url: URL, name: String?, into directory: URL) throws -> URL? {
        var fileName = url.lastPathComponent
        if let name, !name.isEmpty {
            let ext = url.pathExtension
            fileName = ext.isEmpty || name.lowercased().hasSuffix("." + ext.lowercased()) ? name : name + "." + ext
        }
        let folder = directory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let destination = folder.appending(path: fileName.replacingOccurrences(of: "/", with: "_"))
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        try FileManager.default.copyItem(at: url, to: destination)
        return destination
    }
}

import ReminisLinkKit
import UIKit

/// Share extension (docs/specs/06-app-ios.md §4.8). It copies the shared
/// files into the app group (`ShareInbox`), then opens the app on the Add
/// screen, prefilled. Web links are dropped, whatever their representation
/// (`ShareAttachments`): the console never downloads a link (removed from
/// v1). It never talks to a console: uploads need more memory and time than
/// an extension gets. Files are copied by the file system, never loaded in
/// memory.
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
        // Any size of sheet (iPhone, iPad, Mac; M15): centred, at a
        // readable width.
        NSLayoutConstraint.activate([
            stack.centerYAnchor.constraint(equalTo: view.centerYAnchor),
            stack.leadingAnchor.constraint(equalTo: view.readableContentGuide.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: view.readableContentGuide.trailingAnchor),
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
            // Only real files: links are dropped, and an attachment that
            // cannot be read does not stop the others.
            let shared = await ShareAttachments.copyFiles(from: providers, into: scratch)
            let files = shared.files
            guard !files.isEmpty else {
                show(shared.failures > 0
                    ? String(localized: "The shared items could not be copied.")
                    : String(localized: "Nothing to add: share files."))
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
}

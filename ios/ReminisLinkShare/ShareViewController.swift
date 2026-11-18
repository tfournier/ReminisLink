import UIKit

/// Share extension skeleton (docs/specs/06-app-ios.md §4.8). In milestone
/// M4 it will store the shared files and links in the app group and open
/// the app on the Add screen; it never uploads anything itself.
final class ShareViewController: UIViewController {
    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        // TODO(M4): save the request in the app group, then open the app.
        extensionContext?.completeRequest(returningItems: nil)
    }
}

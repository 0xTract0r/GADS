import ReplayKit
import UIKit

@main
final class AppDelegate: UIResponder, UIApplicationDelegate {
    var window: UIWindow?
    private weak var broadcastPicker: RPSystemBroadcastPickerView?
    private weak var statusLabel: UILabel?

    private var uploadExtensionBundleID: String {
        let hostBundleID = Bundle.main.bundleIdentifier ?? "com.example.gads.broadcast"
        return "\(hostBundleID).gads-broadcast-extension"
    }

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        let window = UIWindow(frame: UIScreen.main.bounds)
        let viewController = UIViewController()
        viewController.view.backgroundColor = .systemBackground

        let label = UILabel()
        label.translatesAutoresizingMaskIntoConstraints = false
        label.text = "Tap the broadcast button below"
        label.font = .preferredFont(forTextStyle: .title2)
        label.numberOfLines = 0
        label.textAlignment = .center
        viewController.view.addSubview(label)

        let picker = RPSystemBroadcastPickerView(frame: .zero)
        picker.translatesAutoresizingMaskIntoConstraints = false
        picker.preferredExtension = uploadExtensionBundleID
        picker.showsMicrophoneButton = false
        viewController.view.addSubview(picker)
        self.broadcastPicker = picker

        let startButton = UIButton(type: .system)
        startButton.translatesAutoresizingMaskIntoConstraints = false
        startButton.setTitle("Start Broadcast", for: .normal)
        startButton.titleLabel?.font = .preferredFont(forTextStyle: .title3)
        startButton.accessibilityIdentifier = "gadsStartBroadcastButton"
        startButton.addTarget(self, action: #selector(startBroadcast), for: .touchUpInside)
        viewController.view.addSubview(startButton)
        self.statusLabel = label

        let helper = UILabel()
        helper.translatesAutoresizingMaskIntoConstraints = false
        helper.text = "Broadcast extension: gads-broadcast-extension"
        helper.font = .preferredFont(forTextStyle: .footnote)
        helper.textColor = .secondaryLabel
        helper.textAlignment = .center
        helper.numberOfLines = 0
        viewController.view.addSubview(helper)

        NSLayoutConstraint.activate([
            label.centerXAnchor.constraint(equalTo: viewController.view.centerXAnchor),
            label.centerYAnchor.constraint(equalTo: viewController.view.centerYAnchor, constant: -80),
            label.leadingAnchor.constraint(greaterThanOrEqualTo: viewController.view.leadingAnchor, constant: 24),
            label.trailingAnchor.constraint(lessThanOrEqualTo: viewController.view.trailingAnchor, constant: -24),
            picker.centerXAnchor.constraint(equalTo: viewController.view.centerXAnchor),
            picker.topAnchor.constraint(equalTo: label.bottomAnchor, constant: 24),
            picker.widthAnchor.constraint(equalToConstant: 80),
            picker.heightAnchor.constraint(equalToConstant: 80),
            startButton.centerXAnchor.constraint(equalTo: viewController.view.centerXAnchor),
            startButton.topAnchor.constraint(equalTo: picker.bottomAnchor, constant: 18),
            helper.topAnchor.constraint(equalTo: startButton.bottomAnchor, constant: 20),
            helper.leadingAnchor.constraint(equalTo: viewController.view.leadingAnchor, constant: 24),
            helper.trailingAnchor.constraint(equalTo: viewController.view.trailingAnchor, constant: -24),
        ])

        window.rootViewController = viewController
        window.makeKeyAndVisible()
        self.window = window
        return true
    }

    @objc
    private func startBroadcast() {
        guard let button = broadcastPicker?.subviews.compactMap({ $0 as? UIButton }).first else {
            statusLabel?.text = "Broadcast picker unavailable"
            return
        }
        statusLabel?.text = "Opening broadcast picker..."
        button.sendActions(for: .touchDown)
        button.sendActions(for: .touchUpInside)
    }
}

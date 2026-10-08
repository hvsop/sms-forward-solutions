import SwiftUI
import UserNotifications

extension Notification.Name {
    static let pushReceived = Notification.Name("privateSMS.pushReceived")
    static let deviceTokenChanged = Notification.Name("privateSMS.deviceTokenChanged")
    static let pushRegistrationFailed = Notification.Name("privateSMS.pushRegistrationFailed")
}

@MainActor
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    static var latestToken: String?

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        let token = deviceToken.map { String(format: "%02x", $0) }.joined()
        Self.latestToken = token
        NotificationCenter.default.post(name: .deviceTokenChanged, object: token)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        NotificationCenter.default.post(name: .pushRegistrationFailed, object: nil)
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                           withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        Task { @MainActor in NotificationCenter.default.post(name: .pushReceived, object: nil) }
        completionHandler([.banner, .sound, .list, .badge])
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                           withCompletionHandler completionHandler: @escaping () -> Void) {
        Task { @MainActor in NotificationCenter.default.post(name: .pushReceived, object: nil) }
        completionHandler()
    }
}

@main
@MainActor
struct PrivateSMSApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) var delegate
    @StateObject private var model = InboxModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            InboxView(model: model)
                .overlay {
                    if scenePhase != .active {
                        ZStack {
                            Color(.systemBackground).ignoresSafeArea()
                            Label("private-sms-server", systemImage: "lock.shield.fill").font(.title2)
                        }
                    }
                }
                .task { await model.unlock() }
                .onChange(of: scenePhase) { _, phase in
                    if phase == .background { model.lock() }
                    if phase == .active { Task { await model.unlock() } }
                }
                .onReceive(NotificationCenter.default.publisher(for: .deviceTokenChanged)) { note in
                    if let token = note.object as? String { Task { await model.uploadDevice(token) } }
                }
                .onReceive(NotificationCenter.default.publisher(for: .pushReceived)) { _ in
                    Task { await model.refresh() }
                }
                .onReceive(NotificationCenter.default.publisher(for: .pushRegistrationFailed)) { _ in
                    model.notificationStatus = "系统推送注册失败，请检查网络及签名中的推送权限"
                }
        }
    }
}

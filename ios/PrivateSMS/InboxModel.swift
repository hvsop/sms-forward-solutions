import Foundation
import AuthenticationServices
import Combine
import LocalAuthentication
import UIKit
import UserNotifications

@MainActor
final class InboxModel: ObservableObject {
    @Published var messages: [SMSMessage] = []
    @Published var unreadCount = 0
    private var unreadState = UnreadState()
    private var badgeTask: Task<Void, Never>?
    private var readingIDs = Set<String>()
    @Published var status: ServerStatus?
    @Published var profile: AccountProfile?
    @Published var pairingCode: PairingCode?
    @Published var isManagingDevices = false
    @Published var isUnlocked = false
    @Published var isLoading = false
    @Published var isConnecting = false
    @Published var isClearingMessages = false
    @Published var error: String?
    @Published var notificationStatus = "尚未注册"
    @Published var hasConnection = false
    @Published var usesAppleLogin = false
    @Published var nextBefore = ""
    @Published var endpoint = ""

    private var client: APIClient?
    private var isAuthenticating = false
    private var epoch = UUID()
    private var loadID: UUID?

    init() {
        // Do not expose an existing endpoint or token until device authentication succeeds.
        do { hasConnection = try Keychain.load() != nil }
        catch { hasConnection = true; self.error = "请解锁设备后重试。" }
    }

    func lock() {
        epoch = UUID()
        loadID = nil
        isLoading = false
        isClearingMessages = false
        isUnlocked = false
        messages = []
        unreadState = UnreadState(); unreadCount = 0; readingIDs = []
        status = nil
        profile = nil; pairingCode = nil; isManagingDevices = false
        client = nil
        endpoint = ""
        usesAppleLogin = false
        nextBefore = ""
    }

    func unlock() async {
        guard !isUnlocked, !isAuthenticating else { return }
        isAuthenticating = true
        defer { isAuthenticating = false }
        let attempt = epoch
        do {
            if hasConnection {
                let context = LAContext()
                context.localizedCancelTitle = "取消"
                let success = try await context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: "解锁你的私人短信收件箱")
                guard success else { throw ClientError.authentication }
            }
            guard attempt == epoch else { return }
            if let connection = try Keychain.load() {
                let savedClient = try APIClient(connection: connection)
                if let user = connection.appleUser,
                   let appleState = try? await ASAuthorizationAppleIDProvider().credentialState(forUserID: user),
                   appleState == .revoked || appleState == .notFound {
                    try? await savedClient.logout()
                    guard attempt == epoch else { return }
                    try Keychain.delete()
                    hasConnection = false; isUnlocked = true; usesAppleLogin = false
                    error = "Apple 登录授权已失效，请重新登录。"
                    return
                }
                guard attempt == epoch else { return }
                client = savedClient
                endpoint = connection.endpoint
                usesAppleLogin = connection.appleUser != nil
                hasConnection = true
            } else { hasConnection = false }
            isUnlocked = true
            error = nil
            await refresh()
            if attempt == epoch, isUnlocked, hasConnection { await registerNotifications(requestPermission: false) }
        } catch {
            if attempt == epoch { self.error = "解锁失败，请点击重试。" }
        }
    }

    func connect(endpoint: String, token: String) async -> Bool {
        await connect(Connection(endpoint: endpoint.trimmingCharacters(in: .whitespacesAndNewlines), token: token.trimmingCharacters(in: .whitespacesAndNewlines)))
    }

    private func connect(_ connection: Connection) async -> Bool {
        guard isUnlocked, !isConnecting else { return false }
        isConnecting = true
        defer { isConnecting = false }
        let attempt = epoch
        do {
            let candidate = try APIClient(connection: connection)
            let health = try await candidate.status()
            guard attempt == epoch, isUnlocked else { return false }
            if let old = client, old.connection != connection, let device = AppDelegate.latestToken {
                do { try await old.unregister(token: device) }
                catch ClientError.response(401) { /* Old account was already revoked. */ }
                guard attempt == epoch, isUnlocked else { return false }
            }
            try Keychain.save(connection)
            // Invalidate all requests made using the old connection.
            epoch = UUID(); loadID = nil; isLoading = false
            client = candidate
            self.endpoint = connection.endpoint
            usesAppleLogin = connection.appleUser != nil
            hasConnection = true
            status = health
            messages = []; nextBefore = ""; error = nil
            unreadState = UnreadState(); unreadCount = 0; readingIDs = []
            await refresh()
            await registerNotifications(requestPermission: true)
            return true
        } catch {
            if attempt == epoch { self.error = Self.safeDescription(error) }
            return false
        }
    }

    func refresh() async {
        guard isUnlocked, let client else { return }
        let attempt = epoch
        let request = UUID(); loadID = request; isLoading = true
        defer { if loadID == request { isLoading = false; loadID = nil } }
        do {
            let page = try await client.messages()
            guard isUnlocked, epoch == attempt, loadID == request else { return }
            if unreadState.accepts(page.revision) {
                messages = page.messages; nextBefore = page.nextBefore; error = nil
                updateUnread(count: page.badge, revision: page.revision)
            }
            let newStatus = try await client.status()
            if isUnlocked, epoch == attempt, loadID == request {
                status = newStatus
                updateUnread(count: newStatus.badge, revision: newStatus.revision)
            }
            let account = try await client.profile()
            if isUnlocked, epoch == attempt, loadID == request { profile = account }
        } catch {
            if isUnlocked, epoch == attempt, loadID == request { self.error = Self.safeDescription(error) }
        }
    }

    private func updateUnread(count: Int?, revision: String?) {
        guard unreadState.apply(count: count, revision: revision) else { return }
        unreadCount = unreadState.count
        enqueueBadge(unreadCount)
    }

    private func enqueueBadge(_ count: Int) {
        let previous = badgeTask
        let attempt = epoch
        badgeTask = Task { @MainActor [weak self] in
            await previous?.value
            guard let self else { return }
            do { try await UNUserNotificationCenter.current().setBadgeCount(count) }
            catch { if self.epoch == attempt { self.notificationStatus = "角标更新失败，请检查系统通知设置并重新打开应用。" } }
        }
    }

    // Unstructured task intentionally survives the detail view disappearing.
    // Epoch checks still prevent results from crossing account/lock boundaries.
    func beginReading(_ message: SMSMessage) {
        Task { await markRead(message) }
    }

    private func markRead(_ message: SMSMessage) async {
        guard isUnlocked, let client, !readingIDs.contains(message.id) else { return }
        let attempt = epoch
        readingIDs.insert(message.id)
        defer { if epoch == attempt { readingIDs.remove(message.id) } }
        do {
            let receipt = try await client.markRead(message.id)
            guard isUnlocked, epoch == attempt else { return }
            // Read is monotonic, even if a newer count arrived while this request was in flight.
            if let index = messages.firstIndex(where: { $0.id == message.id }) { messages[index].read = true }
            updateUnread(count: receipt.badge, revision: receipt.revision)
            error = nil
        } catch {
            if isUnlocked, epoch == attempt { self.error = "已读状态同步失败，请检查连接后点击重试。" }
        }
    }

    func loadMore() async {
        guard isUnlocked, !isLoading, let client, !nextBefore.isEmpty else { return }
        let attempt = epoch
        let request = UUID(); loadID = request; isLoading = true
        defer { if loadID == request { isLoading = false; loadID = nil } }
        do {
            let page = try await client.messages(before: nextBefore)
            guard isUnlocked, epoch == attempt, loadID == request else { return }
            guard unreadState.accepts(page.revision) else { return }
            updateUnread(count: page.badge, revision: page.revision)
            let existing = Set(messages.map(\.id))
            messages += page.messages.filter { !existing.contains($0.id) }
            nextBefore = page.nextBefore
        } catch {
            if isUnlocked, epoch == attempt, loadID == request { self.error = Self.safeDescription(error) }
        }
    }

    func delete(_ message: SMSMessage) async {
        guard isUnlocked, let client else { return }
        let attempt = epoch
        do {
            let receipt = try await client.delete(message)
            guard isUnlocked, epoch == attempt else { return }
            messages.removeAll { $0.id == message.id }
            updateUnread(count: receipt.badge, revision: receipt.revision)
            await refresh()
        } catch {
            if isUnlocked, epoch == attempt { self.error = Self.safeDescription(error) }
        }
    }

    func clearMessages() async -> Bool {
        guard isUnlocked, !isClearingMessages, let client else { return false }
        // Prevent an older refresh from repopulating the inbox after deletion.
        epoch = UUID(); loadID = nil; isLoading = false
        let attempt = epoch
        isClearingMessages = true
        defer { if epoch == attempt { isClearingMessages = false } }
        do {
            let receipt = try await client.clearMessages()
            guard isUnlocked, epoch == attempt else { return true }
            messages = []; nextBefore = ""; error = nil
            unreadState = UnreadState(); unreadCount = 0; readingIDs = []
            updateUnread(count: receipt.badge, revision: receipt.revision)
            UNUserNotificationCenter.current().removeAllDeliveredNotifications()
            await refresh()
            return true
        } catch {
            if epoch == attempt { self.error = Self.safeDescription(error) }
            return false
        }
    }

    func registerNotifications(requestPermission: Bool) async {
        guard isUnlocked, hasConnection else { return }
        let attempt = epoch
        let center = UNUserNotificationCenter.current()
        do {
            let existing = await center.notificationSettings()
            // Upgrade existing installations that originally requested only alerts and sound.
            if requestPermission || existing.authorizationStatus == .authorized || existing.authorizationStatus == .provisional {
                _ = try await center.requestAuthorization(options: [.alert, .sound, .badge])
            }
            let settings = await center.notificationSettings()
            guard isUnlocked, epoch == attempt else { return }
            switch settings.authorizationStatus {
            case .authorized, .provisional, .ephemeral:
                notificationStatus = "已允许通知，正在注册"
                UIApplication.shared.registerForRemoteNotifications()
                if let token = AppDelegate.latestToken { await uploadDevice(token) }
            case .denied: notificationStatus = "通知被关闭，请在系统设置中开启"
            default: notificationStatus = "尚未允许通知"
            }
        } catch { if epoch == attempt { notificationStatus = "无法申请通知权限，请重试" } }
    }

    func uploadDevice(_ token: String) async {
        guard isUnlocked, let client else { return }
        let attempt = epoch
        do {
            try await client.register(token: token)
            let current = try await client.status()
            guard epoch == attempt, isUnlocked else { return }
            status = current
            updateUnread(count: current.badge, revision: current.revision)
            notificationStatus = current.apnsConfigured ? "设备已注册" : "设备已注册，服务端尚未配置 APNs"
        } catch { if epoch == attempt, isUnlocked { notificationStatus = Self.safeDescription(error) } }
    }

    func signInWithApple(_ credential: ASAuthorizationAppleIDCredential, challenge: AppleChallenge, invite: String, linking: Bool) async -> Bool {
        guard isUnlocked, !isConnecting, let identityData = credential.identityToken,
              let codeData = credential.authorizationCode,
              let identity = String(data: identityData, encoding: .utf8),
              let code = String(data: codeData, encoding: .utf8),
              credential.state == challenge.challengeID else { error = "Apple 登录未完成，请重试。"; return false }
        let attempt = epoch
        isConnecting = true
        do {
            let api: APIClient
            if linking {
                guard let current = client, current.connection.endpoint == APIClient.serviceEndpoint else {
                    throw ClientError.service("请先连接 sms.example.com 上的原账户，再绑定 Apple 登录。")
                }
                api = current
            } else { api = try APIClient.authClient() }
            let token = try await api.appleLogin(challenge: challenge, identity: identity, code: code, invite: invite, linking: linking)
            guard epoch == attempt, isUnlocked else { isConnecting = false; return false }
            isConnecting = false
            return await connect(Connection(endpoint: APIClient.serviceEndpoint, token: token, appleUser: credential.user))
        } catch {
            isConnecting = false
            if epoch == attempt { self.error = Self.safeDescription(error) }
            return false
        }
    }

    func refreshProfile() async {
        guard isUnlocked, let client else { return }
        let attempt = epoch
        do {
            let value = try await client.profile()
            guard epoch == attempt, isUnlocked else { return }
            if let old = profile, old.androidDeviceID != value.androidDeviceID { pairingCode = nil }
            profile = value
        } catch { if epoch == attempt { self.error = Self.safeDescription(error) } }
    }

    func createPairing() async {
        guard isUnlocked, !isManagingDevices, let client else { return }
        let attempt = epoch; isManagingDevices = true
        defer { if epoch == attempt { isManagingDevices = false } }
        do {
            let code = try await client.pairing()
            guard epoch == attempt, isUnlocked else { return }
            pairingCode = code; error = nil
        } catch { if epoch == attempt { self.error = Self.safeDescription(error) } }
    }

    func unpairAndroid() async {
        guard isUnlocked, !isManagingDevices, let client else { return }
        let attempt = epoch; isManagingDevices = true
        defer { if epoch == attempt { isManagingDevices = false } }
        do {
            try await client.unpairAndroid()
            guard epoch == attempt, isUnlocked else { return }
            pairingCode = nil; error = nil; await refreshProfile()
        } catch { if epoch == attempt { self.error = Self.safeDescription(error) } }
    }

    func logout() async {
        guard isUnlocked, let client else { return }
        let attempt = epoch
        do {
            try await client.logout()
            guard epoch == attempt else { return }
            try Keychain.delete(); lock(); hasConnection = false; isUnlocked = true
            notificationStatus = "尚未注册"; error = nil
            UNUserNotificationCenter.current().removeAllDeliveredNotifications()
            enqueueBadge(0)
        } catch { if epoch == attempt { self.error = Self.safeDescription(error) } }
    }

    private static func safeDescription(_ error: Error) -> String {
        if let own = error as? ClientError { return own.localizedDescription }
        if error is URLError { return "连接失败，请检查网络和服务地址，或稍后重试。" }
        return "操作失败，请稍后重试。"
    }

    func deleteAccount() async -> Bool {
        guard isUnlocked, let client else { return false }
        let attempt = epoch
        do {
            try await client.deleteAccount()
            guard epoch == attempt else { return true }
            try Keychain.delete()
            lock()
            hasConnection = false
            isUnlocked = true
            notificationStatus = "尚未注册"
            UNUserNotificationCenter.current().removeAllDeliveredNotifications()
            enqueueBadge(0)
            error = nil
            return true
        } catch {
            if epoch == attempt { self.error = Self.safeDescription(error) }
            return false
        }
    }
}

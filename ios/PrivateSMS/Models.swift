import Foundation

struct SMSMessage: Decodable, Identifiable, Hashable {
    let id: String
    let sender: String
    let content: String
    let sim: String
    let sourceTime: String
    let receivedAt: String
    var read: Bool?
    var isUnread: Bool { read != true }

    enum CodingKeys: String, CodingKey {
        case id, sender, content, sim, read
        case sourceTime = "source_time", receivedAt = "received_at"
    }

    var dateLabel: String {
        let parser = ISO8601DateFormatter()
        parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let date = parser.date(from: receivedAt) ?? ISO8601DateFormatter().date(from: receivedAt)
        return date?.formatted(date: .abbreviated, time: .shortened) ?? receivedAt
    }

    var verificationCode: String? {
        // Avoid labeling every phone number or amount as an OTP.
        guard content.range(of: "验证码|驗證碼|校验码|校驗碼|verification|one.time|OTP|code", options: .regularExpression.union(.caseInsensitive)) != nil,
              let range = content.range(of: "(?<![0-9])[0-9]{4,8}(?![0-9])", options: .regularExpression) else { return nil }
        return String(content[range])
    }
}

// Navigation identity stays stable while the message's renderable value changes.
// SMSMessage itself uses full-value equality so List notices read-state updates.
struct MessageRoute: Hashable {
    let message: SMSMessage
    static func == (lhs: MessageRoute, rhs: MessageRoute) -> Bool { lhs.message.id == rhs.message.id }
    func hash(into hasher: inout Hasher) { hasher.combine(message.id) }
}

struct MessagePage: Decodable {
    let messages: [SMSMessage]
    let nextBefore: String
    let snapshotSequence: String?
    let badge: Int?
    let revision: String?
    enum CodingKeys: String, CodingKey { case messages, badge, revision; case nextBefore = "next_before", snapshotSequence = "snapshot_sequence" }
}

struct ServerStatus: Decodable {
    let messages: Int
    let pendingNotifications: Int
    let deviceRegistered: Bool
    let apnsConfigured: Bool
    let lastPushError: String
    let badge: Int?
    let revision: String?
    enum CodingKeys: String, CodingKey {
        case messages, badge, revision
        case pendingNotifications = "pending_notifications", deviceRegistered = "device_registered"
        case apnsConfigured = "apns_configured", lastPushError = "last_push_error"
    }
}

struct Connection: Codable, Equatable {
    let endpoint: String
    let token: String
    var appleUser: String? = nil
}

enum ClientError: LocalizedError {
    case invalidConnection, response(Int), keychain, invalidResponse, authentication, service(String)
    var errorDescription: String? {
        switch self {
        case .service(let message): return message
        case .invalidConnection: return "请输入 HTTPS 服务地址（不含路径、参数或密码）及 64 位读取令牌。"
        case .response(401): return "读取令牌无效或账户已注销，请检查令牌或联系服务管理员。"
        case .response(409): return "推送配置冲突：请检查 sandbox／production 环境，或先解除此设备与旧账户的绑定。"
        case .response(let status): return "服务端返回错误（\(status)），请稍后重试。"
        case .keychain: return "无法读取或保存钥匙串，请在设备解锁后重试。"
        case .invalidResponse: return "服务端响应无效。"
        case .authentication: return "请使用 Face ID、Touch ID 或设备密码解锁。"
        }
    }
}

struct AppleChallenge: Decodable {
    let challengeID: String
    let nonce: String
    let expiresAt: String
    enum CodingKeys: String, CodingKey { case challengeID = "challenge_id", nonce, expiresAt = "expires_at" }
}
struct PairingCode: Decodable {
    let code: String
    let expiresAt: String
    enum CodingKeys: String, CodingKey { case code, expiresAt = "expires_at" }
    var expiry: Date { ISO8601DateFormatter().date(from: expiresAt) ?? {
        let p = ISO8601DateFormatter(); p.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return p.date(from: expiresAt) ?? .distantPast
    }() }
}
struct AccountProfile: Decodable {
    let appleLinked: Bool
    let androidPaired: Bool
    let androidDeviceID: String
    let androidPairedAt: String
    let androidLastSeen: String
    let appleAvailable: Bool
    enum CodingKeys: String, CodingKey {
        case appleLinked = "apple_linked", androidPaired = "android_paired", androidDeviceID = "android_device_id"
        case androidPairedAt = "android_paired_at", androidLastSeen = "android_last_seen", appleAvailable = "apple_available"
    }
    var lastSeenLabel: String {
        guard !androidLastSeen.hasPrefix("0001") else { return "尚无连接记录" }
        let p = ISO8601DateFormatter(); p.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return (p.date(from: androidLastSeen) ?? ISO8601DateFormatter().date(from: androidLastSeen))?
            .formatted(date: .abbreviated, time: .shortened) ?? "尚无连接记录"
    }
}

struct BadgeState: Decodable {
    let badge: Int
    let revision: String
}

// Reject an older network response after a read, delete, or newer message arrival.
struct UnreadState {
    private(set) var revision: UInt64 = 0
    private(set) var count = 0
    func accepts(_ value: String?) -> Bool {
        guard let value, let number = UInt64(value) else { return false }
        return number >= revision
    }
    mutating func apply(count: Int?, revision value: String?) -> Bool {
        guard let count, count >= 0, accepts(value), let value, let number = UInt64(value) else { return false }
        revision = number; self.count = count
        return true
    }
}

// Release values can be supplied as Xcode build settings without changing source.
enum AppConfiguration {
    private static func value(_ key: String) -> String {
        let value = (Bundle.main.object(forInfoDictionaryKey: key) as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        return value.contains("$(") ? "" : value
    }
    static var defaultEndpoint: String { value("SMSDefaultEndpoint") }
    static var appleLoginEndpoint: String? {
        let endpoint = value("SMSAppleLoginEndpoint")
        guard let url = URL(string: endpoint), url.scheme == "https", url.host != nil else { return nil }
        return endpoint
    }
    static var privacyURL: URL { URL(string: "https://github.com/hvsop/sms-forward-solutions/blob/main/docs/privacy.md")! }
    static var helpURL: URL { URL(string: "https://github.com/hvsop/sms-forward-solutions/blob/main/docs/usage.md")! }
    static var setupURL: URL { URL(string: "https://github.com/hvsop/sms-forward-solutions#从零开始")! }
    static var androidDownloadURL: URL {
        if let url = URL(string: value("SMSAndroidDownloadURL")), url.scheme == "https", url.host != nil { return url }
        return URL(string: "https://github.com/hvsop/sms-forward-solutions/blob/main/docs/android.md")!
    }
}

import Foundation

private final class NoRedirectDelegate: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

final class APIClient {
    let connection: Connection
    private let base: URL
    private let delegate = NoRedirectDelegate()
    private let session: URLSession

    init(connection: Connection) throws {
        guard let parts = URLComponents(string: connection.endpoint), parts.scheme == "https",
              let host = parts.host, !host.isEmpty,
              parts.user == nil, parts.password == nil, parts.query == nil, parts.fragment == nil,
              parts.path == "" || parts.path == "/", let base = parts.url,
              connection.token.range(of: "^[a-fA-F0-9]{64}$", options: .regularExpression) != nil else {
            throw ClientError.invalidConnection
        }
        self.connection = connection
        self.base = base
        let config = URLSessionConfiguration.ephemeral
        config.urlCache = nil
        config.httpCookieStorage = nil
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        config.timeoutIntervalForRequest = 15
        config.timeoutIntervalForResource = 25
        session = URLSession(configuration: config, delegate: delegate, delegateQueue: nil)
    }

    deinit { session.invalidateAndCancel() }

    private func request(_ path: String, method: String = "GET", query: [URLQueryItem] = [], body: Data? = nil, authenticated: Bool = true) async throws -> Data {
        var url = URLComponents(url: base.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { url.queryItems = query }
        var request = URLRequest(url: url.url!)
        request.httpMethod = method
        if authenticated { request.setValue("Bearer \(connection.token)", forHTTPHeaderField: "Authorization") }
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = body
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw ClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            if let value = try? JSONDecoder().decode([String: String].self, from: data),
               let code = value["error"], let message = Self.authErrors[code] { throw ClientError.service(message) }
            throw ClientError.response(http.statusCode)
        }
        return data
    }

    func messages(before: String = "") async throws -> MessagePage {
        var query = [URLQueryItem(name: "limit", value: "50")]
        if !before.isEmpty { query.append(URLQueryItem(name: "before", value: before)) }
        return try JSONDecoder().decode(MessagePage.self, from: await request("v1/messages", query: query))
    }

    func markRead(_ id: String) async throws -> BadgeState {
        try JSONDecoder().decode(BadgeState.self, from: await request("v1/messages/\(id)/read", method: "PUT"))
    }

    func status() async throws -> ServerStatus {
        try JSONDecoder().decode(ServerStatus.self, from: await request("v1/status"))
    }

    func register(token: String) async throws {
        #if DEBUG
        let environment = "sandbox"
        #else
        let environment = "production"
        #endif
        let body = try JSONEncoder().encode(["token": token, "environment": environment])
        _ = try await request("v1/device", method: "PUT", body: body)
    }

    func delete(_ message: SMSMessage) async throws -> BadgeState {
        try JSONDecoder().decode(BadgeState.self, from: await request("v1/messages/\(message.id)", method: "DELETE"))
    }

    func unregister(token: String) async throws {
        let body = try JSONEncoder().encode(["token": token])
        _ = try await request("v1/device", method: "DELETE", body: body)
    }

    func clearMessages() async throws -> BadgeState {
        try JSONDecoder().decode(BadgeState.self, from: await request("v1/messages", method: "DELETE"))
    }

    func deleteAccount() async throws {
        let body = try JSONEncoder().encode(["confirmation": "delete-account"])
        _ = try await request("v1/account", method: "DELETE", body: body)
    }
    static let serviceEndpoint = "https://sms.example.com"
    private static let authErrors = [
        "apple_not_configured": "Apple 登录尚未启用，请稍后重试；旧用户仍可通过令牌连接。",
        "apple_identity_invalid": "Apple 登录验证未完成，请重新点击登录。",
        "challenge_expired": "登录请求已过期，请重试。",
        "invite_required": "首次开通需要有效的试用邀请码。邀请码只能使用一次，原有账户登录无需邀请码。",
        "apple_account_conflict": "这个 Apple 账户已关联其他收件箱，或当前收件箱已绑定其他 Apple 账户。请使用原账户。",
        "apple_link_required": "请先绑定 Apple 账户，以便退出后重新登录。"
    ]
    static func authClient() throws -> APIClient {
        try APIClient(connection: Connection(endpoint: serviceEndpoint, token: String(repeating: "0", count: 64)))
    }
    static func challenge() async throws -> AppleChallenge {
        let api = try authClient()
        return try JSONDecoder().decode(AppleChallenge.self, from: await api.request("v1/auth/challenge", method: "POST", authenticated: false))
    }
    func appleLogin(challenge: AppleChallenge, identity: String, code: String, invite: String, linking: Bool) async throws -> String {
        let body = try JSONEncoder().encode(["challenge_id": challenge.challengeID, "identity_token": identity,
                                            "authorization_code": code, "invite_code": invite])
        let response = try await request("v1/auth/apple", method: "POST", body: body, authenticated: linking)
        struct Result: Decodable { let read_token: String }
        return try JSONDecoder().decode(Result.self, from: response).read_token
    }
    func profile() async throws -> AccountProfile {
        try JSONDecoder().decode(AccountProfile.self, from: await request("v1/account/profile"))
    }
    func pairing() async throws -> PairingCode {
        try JSONDecoder().decode(PairingCode.self, from: await request("v1/android/pairing", method: "POST"))
    }
    func unpairAndroid() async throws { _ = try await request("v1/android/device", method: "DELETE") }
    func logout() async throws { _ = try await request("v1/account/logout", method: "POST") }

}

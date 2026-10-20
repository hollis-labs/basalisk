import Foundation
import Security

enum CompanionError: LocalizedError {
    case invalidOrigin, missingCredential, keychain(OSStatus), response(Int), oversizedResponse
    var errorDescription: String? {
        switch self {
        case .invalidOrigin: return "Use an HTTPS server origin, or HTTP localhost through your SSH tunnel. Do not include a path."
        case .missingCredential: return "Pair this Mac with Basalisk first."
        case .keychain: return "The credential could not be read or saved in Keychain."
        case .response(let status):
            if status == 401 { return "The credential or pairing code is no longer valid. Pair again." }
            if status == 429 { return "Pairing is rate limited. Wait a minute and try again." }
            return "Basalisk returned HTTP \(status)."
        case .oversizedResponse: return "The server response was too large."
        }
    }
}

func serverOrigin(_ raw: String) throws -> String {
    guard var u = URLComponents(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
          let scheme = u.scheme?.lowercased(), let host = u.host?.lowercased(), !host.isEmpty,
          u.user == nil, u.password == nil, u.query == nil, u.fragment == nil,
          u.path.isEmpty || u.path == "/" else { throw CompanionError.invalidOrigin }
    let loopback = host == "localhost" || host == "127.0.0.1" || host == "[::1]" || host == "::1"
    guard scheme == "https" || (scheme == "http" && loopback),
          u.port == nil || (1...65535).contains(u.port!) else { throw CompanionError.invalidOrigin }
    u.scheme = scheme; u.host = host; u.path = ""
    guard let origin = u.string, URL(string: origin) != nil else { throw CompanionError.invalidOrigin }
    return origin
}

struct Credential: Codable {
    let token: String
    let deviceID: String
    var expiresAt: String
}

enum Keychain {
    static let service = "com.hollislabs.basalisk.companion"
    static func query(_ origin: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
         kSecAttrAccount as String: origin]
    }
    static func read(_ origin: String) throws -> Credential? {
        var q = query(origin)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw CompanionError.keychain(status) }
        return try JSONDecoder().decode(Credential.self, from: data)
    }
    static func save(_ credential: Credential, origin: String) throws {
        let data = try JSONEncoder().encode(credential)
        let update = SecItemUpdate(query(origin) as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if update == errSecSuccess { return }
        guard update == errSecItemNotFound else { throw CompanionError.keychain(update) }
        var q = query(origin)
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(q as CFDictionary, nil)
        guard status == errSecSuccess else { throw CompanionError.keychain(status) }
    }
    static func remove(_ origin: String) throws {
        let status = SecItemDelete(query(origin) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw CompanionError.keychain(status) }
    }
}

final class NoRedirects: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}

final class Transport {
    private let session: URLSession
    init(configuration cfg: URLSessionConfiguration = .ephemeral) {
        cfg.httpCookieStorage = nil; cfg.urlCache = nil; cfg.httpShouldSetCookies = false
        cfg.timeoutIntervalForRequest = 10
        cfg.timeoutIntervalForResource = 15
        session = URLSession(configuration: cfg, delegate: NoRedirects(), delegateQueue: nil)
    }
    func call<T: Decodable>(_ origin: String, path: String, method: String = "GET",
                            token: String? = nil, body: Data? = nil) async throws -> T {
        guard let url = URL(string: origin + path) else { throw CompanionError.invalidOrigin }
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData)
        request.httpMethod = method; request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("1", forHTTPHeaderField: "Hollis-Pairing-Protocol")
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw CompanionError.response(0) }
        guard (200..<300).contains(http.statusCode) else { throw CompanionError.response(http.statusCode) }
        guard data.count <= 1_048_576 else { throw CompanionError.oversizedResponse }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

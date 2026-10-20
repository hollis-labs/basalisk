import Foundation
import Security
import CryptoKit

// v1 local IPC uses bounded JSON Data, with no credentials or arbitrary URLs.
// Both peers require the other's exact bundle ID and the same signing leaf.
@objc(BasaliskCaptureServiceV1) protocol CaptureServiceV1 {
    func request(_ data: Data, reply: @escaping (Data) -> Void)
}
let captureMachService = "com.hollislabs.basalisk.companion.capture"
func capturePeerRequirement(bundleID: String) throws -> String {
    var code: SecCode?
    var info: CFDictionary?
    var staticCode: SecStaticCode?
    guard SecCodeCopySelf([], &code) == errSecSuccess, let code,
          SecCodeCopyStaticCode(code, [], &staticCode) == errSecSuccess, let staticCode,
          SecCodeCopySigningInformation(staticCode, SecCSFlags(rawValue: kSecCSSigningInformation), &info) == errSecSuccess,
          let certs = (info as? [String: Any])?[kSecCodeInfoCertificates as String] as? [SecCertificate], let leaf = certs.first else {
        throw CaptureFailure("A persistent signing certificate is required for the capture bridge.")
    }
    let hash = Insecure.SHA1.hash(data: SecCertificateCopyData(leaf) as Data).map { String(format: "%02x", $0) }.joined()
    return "identifier \"\(bundleID)\" and certificate leaf = H\"\(hash)\""
}
struct CaptureFailure: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}
struct CaptureReply: Codable {
    let captureID: String?
    let state: String
    let message: String
    let fragmentID: String?
    static func failed(_ message: String) -> CaptureReply { CaptureReply(captureID: nil, state: "rejected", message: message, fragmentID: nil) }
}
struct CaptureRequest: Decodable {
    let operation: String
    let captureID: String?
    let submission: CaptureDraft?
}
struct CaptureDraft: Codable {
    let captureID: String
    let content: String
    let tags: [String]
    let draftAt: String
    let submittedAt: String
    let draft: LocalContext?
    let saveContext: LocalContext?
}

final class CaptureBridge: NSObject, NSXPCListenerDelegate, CaptureServiceV1 {
    private let listener: NSXPCListener
    private let peer: String
    private let handle: @MainActor (Data) -> Data
    init(serviceName: String = captureMachService, handle: @escaping @MainActor (Data) -> Data) throws {
        peer = try capturePeerRequirement(bundleID: "com.hollis-labs.frag")
        self.handle = handle
        listener = NSXPCListener(machServiceName: serviceName)
        super.init()
        listener.delegate = self
        listener.resume()
    }
    deinit { listener.invalidate() }
    func listener(_ listener: NSXPCListener, shouldAcceptNewConnection connection: NSXPCConnection) -> Bool {
        guard connection.effectiveUserIdentifier == getuid() else { return false }
        connection.setCodeSigningRequirement(peer)
        connection.exportedInterface = NSXPCInterface(with: CaptureServiceV1.self)
        connection.exportedObject = self
        connection.resume()
        return true
    }
    func request(_ data: Data, reply: @escaping (Data) -> Void) {
        guard data.count <= 128 * 1024 else {
            reply(try! JSONEncoder().encode(CaptureReply.failed("Note request is too large."))); return
        }
        Task { @MainActor in reply(handle(data)) }
    }
}

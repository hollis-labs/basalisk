import Foundation
import CryptoKit

struct CaptureBinding: Codable {
    let origin: String
    let deviceID: String
    let integrationID: String
    let name: String
}
struct CaptureEnvelope: Codable {
    let captureID: String
    let integrationID: String
    let content: String
    let tags: [String]
    let draftAt: String
    let submittedAt: String
    let draft: LocalContext?
    let saveContext: LocalContext?
    init(_ note: CaptureDraft, integrationID: String) {
        captureID = note.captureID; self.integrationID = integrationID
        content = note.content; tags = note.tags; draftAt = note.draftAt; submittedAt = note.submittedAt
        draft = note.draft; saveContext = note.saveContext
    }
}
struct CaptureEntry: Codable, Identifiable {
    var id: String { captureID }
    let captureID: String
    let fingerprint: String
    let binding: CaptureBinding
    var envelope: CaptureEnvelope?
    var state: String
    var message: String
    var serverAccepted: Bool
    var fragmentID: String?
    var reply: CaptureReply { CaptureReply(captureID: captureID, state: state, message: message, fragmentID: fragmentID) }
}
// All mutations are copy -> persist -> publish. A failed write never acks a
// note or replaces the last durable state. Corruption fails closed, not empty.
@MainActor final class CaptureOutbox {
    private let file: URL
    private(set) var entries: [CaptureEntry]
    init(directory: URL) throws {
        file = directory.appendingPathComponent("captures.json")
        if FileManager.default.fileExists(atPath: file.path) {
            let info = try file.resourceValues(forKeys: [.isRegularFileKey,.isSymbolicLinkKey,.fileSizeKey])
            guard info.isRegularFile == true, info.isSymbolicLink != true, (info.fileSize ?? Int.max) <= 20 * 1024 * 1024 else { throw CaptureFailure("The capture outbox needs repair; existing notes have been preserved.") }
            entries = try JSONDecoder().decode([CaptureEntry].self, from: Data(contentsOf: file))
        } else { entries = [] }
    }
    func accept(_ draft: CaptureDraft, binding: CaptureBinding) throws -> CaptureReply {
        let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_")
        guard (16...100).contains(draft.captureID.utf8.count), draft.captureID.unicodeScalars.allSatisfy({ allowed.contains($0) }),
              !draft.content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, draft.content.utf8.count <= 65536, !draft.content.contains("\0"), draft.tags.count <= 32,
              draft.tags.allSatisfy({ !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && $0.utf8.count <= 100 && $0.unicodeScalars.allSatisfy { !CharacterSet.controlCharacters.contains($0) } }),
              let opened = date(draft.draftAt), let submitted = date(draft.submittedAt), opened <= submitted, submitted <= Date().addingTimeInterval(30) else { throw CaptureFailure("Invalid note. Use at most 64 KiB and 32 tags.") }
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        let fingerprint = SHA256.hash(data: try encoder.encode(draft)).map { String(format: "%02x", $0) }.joined()
        if let old = entries.first(where: { $0.captureID == draft.captureID }) {
            guard old.fingerprint == fingerprint else { throw CaptureFailure("This capture ID already belongs to a different note.") }
            return old.reply // Never rebind an existing operation, even after pairing changes.
        }
        for context in [draft.draft, draft.saveContext].compactMap({ $0 }) {
            guard context.origin == binding.origin, context.deviceID == binding.deviceID else { throw CaptureFailure("This draft belongs to another pairing. Keep or copy it before changing connections.") }
        }
        guard entries.filter({ $0.envelope != nil }).count < 100, entries.count < 1000 else { throw CaptureFailure("The capture outbox is full. Keep your draft and review queued notes.") }
        let entry = CaptureEntry(captureID: draft.captureID, fingerprint: fingerprint, binding: binding,
            envelope: CaptureEnvelope(draft, integrationID: binding.integrationID), state: "queued",
            message: "Queued on this Mac · not yet saved to FE", serverAccepted: false)
        try persist(entries + [entry]); return entry.reply
    }
    func update(_ entry: CaptureEntry) throws {
        guard let index = entries.firstIndex(where: { $0.id == entry.id }) else { return }
        var next = entries; next[index] = entry; try persist(next)
    }
    func forgetCompleted(_ id: String) throws {
        // Pending notes can only be discarded by the owner in Basalisk, so a
        // lost submission ack cannot create a local cancel/delivery race.
        try persist(entries.filter { $0.id != id || $0.envelope != nil })
    }
    private func persist(_ next: [CaptureEntry]) throws {
        try privateWrite(next, to: file)
        let handle = try FileHandle(forWritingTo: file)
        defer { try? handle.close() }
        try handle.synchronize()
        entries = next
    }
}

import Foundation

// Same-user, credential-free bridge for owned native tools. Unknown/stale state
// is absence, never permission to guess a session. Raw history stays local.
struct LocalContext: Codable {
    let schemaVersion: Int
    let origin: String
    let deviceID: String
    let sessionID: String
    let contextID: String
    let contextAsOf: String
    let publishedAt: String
    let expiresAt: String
    let project: String
    let workstream: String
    let precedingApp: String?
    let precedingBundle: String?
    let historyRefs: [String]
}

func localContext(origin: String, deviceID: String, context: ContextResponse?,
                  generation: String, history: [Observation], collecting: Bool,
                  now: Date = Date()) -> LocalContext? {
    guard collecting, let context, let session = context.current, session.status == "active",
          let asOf = date(context.asOf), now.timeIntervalSince(asOf) >= 0,
          now.timeIntervalSince(asOf) < 30 else { return nil }
    let recent = history.filter {
        $0.activitySessionId == session.id && $0.contextId == session.context.id &&
        $0.bundleId != "com.hollis-labs.frag" &&
        date($0.occurredAt).map { $0 <= now && now.timeIntervalSince($0) <= 300 } == true
    }
    let focus = recent.last { $0.kind == "app.activated" }
    return LocalContext(schemaVersion: 1, origin: origin, deviceID: deviceID,
        sessionID: session.id, contextID: session.context.id, contextAsOf: context.asOf,
        publishedAt: stamp(now), expiresAt: stamp(min(now.addingTimeInterval(12), asOf.addingTimeInterval(30))),
        project: session.context.project, workstream: session.context.workstream,
        precedingApp: focus?.appName, precedingBundle: focus?.bundleId,
        historyRefs: recent.suffix(50).map { "\(deviceID):\(generation):\($0.sequence)" })
}

func privateWrite<T: Encodable>(_ value: T, to file: URL) throws {
    let dir = file.deletingLastPathComponent()
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
    try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: dir.path)
    try JSONEncoder().encode(value).write(to: file, options: .atomic)
    try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
}

struct ArtifactReceipt: Codable {
    let captureID: String
    let draftAt: String
    let savedAt: String
    let fragmentID: String
    let fragmentRevisionID: String?
    let fragmentOrigin: String
    let draft: LocalContext
    let saveContext: LocalContext?
}

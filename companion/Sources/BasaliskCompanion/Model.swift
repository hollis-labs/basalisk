import AppKit
import Foundation
import Combine

struct WorkContext: Codable { let id: String; let project: String; let workstream: String }
struct WorkSession: Codable { let id: String; let title: String; let status: String; let context: WorkContext }
struct ContextResponse: Decodable { let current: WorkSession?; let asOf: String }
struct DeviceResponse: Decodable { let id: String; let expires_at: String }
struct PairResponse: Decodable { let device: DeviceResponse; let token: String; let token_type: String }
struct Observation: Codable {
    let sequence: Int64
    let occurredAt: String
    let kind: String
    let bundleId: String
    let appName: String
    let activitySessionId: String
    let contextId: String
    let contextAsOf: String
}
struct QueueState: Codable {
    var origin: String?
    var generation = UUID().uuidString
    var nextSequence: Int64 = 1
    var events: [Observation] = []
    var history: [Observation]?
}
struct Batch: Encodable { let generation: String; let events: [Observation] }
struct Receipt: Decodable { let accepted: Int; let ignored: Int }
enum ConnectionState: Equatable { case unpaired, checking, connected, offline, needsPairing }

func date(_ raw: String) -> Date? {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let result = formatter.date(from: raw) { return result }
    formatter.formatOptions = [.withInternetDateTime]
    return formatter.date(from: raw)
}
func stamp(_ value: Date) -> String {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter.string(from: value)
}

func bounded(_ value: String, bytes: Int) -> String {
    var result = ""
    for scalar in value.unicodeScalars {
        if CharacterSet.controlCharacters.contains(scalar) { continue }
        let next = String(scalar)
        if result.utf8.count + next.utf8.count > bytes { break }
        result += next
    }
    return result
}

@MainActor final class CompanionModel: ObservableObject {
    @Published var originInput: String
    @Published var pairingCode = ""
    @Published var connectedOrigin: String?
    @Published var collecting = false
    @Published var status = "Pair this Mac to get started."
    @Published var error = ""
    @Published var busy = false
    @Published var connectionState: ConnectionState = .unpaired
    @Published var pairingError = ""
    @Published var pairingCompletedAt: Date?
    @Published var lastCheckedAt: Date?
    @Published var lastSentAt: Date?
    @Published var sessionTitle = "No active work session"
    @Published var hasActiveSession = false
    @Published var queued = 0
    @Published var dropped = 0
    @Published var exclusions: String {
        didSet {
            preferences.set(exclusions, forKey: "excludedBundles")
            queue.events.removeAll { excluded($0.bundleId) }
            queue.history?.removeAll { excluded($0.bundleId) }
            saveQueue()
        }
    }
    @Published var captureAllowed = false {
        didSet { preferences.set(captureAllowed, forKey: "allowFragCapture") }
    }
    @Published var captureStatus = "Allow Frag here after pairing with fragment permission."
    @Published var captures: [CaptureEntry] = []
    private var captureOutbox: CaptureOutbox?
    private var captureBinding: CaptureBinding?
    private var captureBridge: CaptureBridge?
    private var syncingCaptures = false
    private let transport: Transport
    private var credential: Credential?
    private var context: ContextResponse?
    private var queue = QueueState()
    private var observers: [NSObjectProtocol] = []
    private var pollTask: Task<Void, Never>?
    private var flushing = false
    private var connectionEpoch = UUID()
    private let storage: URL
    private let preferences: UserDefaults
    private let artifactDirectory: URL
    private let observesWorkspace: Bool

    init(preferences: UserDefaults = .standard, storage: URL? = nil, startServices: Bool = true, transport: Transport = Transport()) {
        self.preferences = preferences
        self.transport = transport
        observesWorkspace = startServices
        originInput = preferences.string(forKey: "serverOrigin") ?? "http://127.0.0.1:18080"
        exclusions = preferences.string(forKey: "excludedBundles") ?? ""
        self.storage = storage ?? FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/BasaliskCompanion", isDirectory: true)
        artifactDirectory = storage?.appendingPathComponent("BasaliskLinks") ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/Frag/BasaliskLinks")
        do {
            try FileManager.default.createDirectory(at: self.storage, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: self.storage.path)
            let stored = self.storage.appendingPathComponent("queue.json")
            if FileManager.default.fileExists(atPath: stored.path) {
                queue = try JSONDecoder().decode(QueueState.self, from: Data(contentsOf: stored))
            }
            if let origin = try? serverOrigin(originInput), let saved = try Keychain.read(origin) {
                connectedOrigin = origin; credential = saved
                connectionState = .checking
                status = "Paired. Capture is paused until you start it."
            }
            if queue.origin != connectedOrigin { queue = QueueState() }
            prune()
        } catch { self.error = "Could not restore private companion state: \(error.localizedDescription)" }
        captureAllowed = preferences.bool(forKey: "allowFragCapture")
        do {
            captureOutbox = try CaptureOutbox(directory: self.storage)
            captures = captureOutbox?.entries ?? []
            let bindingFile = self.storage.appendingPathComponent("capture-binding.json")
            if FileManager.default.fileExists(atPath: bindingFile.path) {
                captureBinding = try JSONDecoder().decode(CaptureBinding.self, from: Data(contentsOf: bindingFile))
            }
        } catch { self.error = "Could not restore the capture outbox. Existing notes have been preserved." }
        guard startServices else { return }
        if ProcessInfo.processInfo.environment["BASALISK_CAPTURE_SERVICE"] == "1" {
            do {
                captureBridge = try CaptureBridge { [weak self] data in
                    let result = self?.handleCapture(data) ?? CaptureReply.failed("Companion is closing. Keep your draft.")
                    return (try? JSONEncoder().encode(result)) ?? Data()
                }
            } catch { self.error = error.localizedDescription }
        } else {
            captureStatus = "The capture bridge requires the signed companion LaunchAgent."
        }
        let center = NSWorkspace.shared.notificationCenter
        observers.append(center.addObserver(forName: NSWorkspace.didActivateApplicationNotification, object: nil, queue: .main) { [weak self] notification in
            guard let app = notification.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else { return }
            let bundle = app.bundleIdentifier ?? "", name = app.localizedName ?? ""
            Task { @MainActor in self?.record(kind: "app.activated", bundle: bundle, name: name) }
        })
        for (notification, kind) in [(NSWorkspace.willSleepNotification, "system.sleep"), (NSWorkspace.didWakeNotification, "system.wake")] {
            observers.append(center.addObserver(forName: notification, object: nil, queue: .main) { [weak self] _ in
                Task { @MainActor in self?.record(kind: kind, bundle: "", name: "") }
            })
        }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.tick()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func excluded(_ bundle: String) -> Bool {
        if bundle == Bundle.main.bundleIdentifier { return true }
        return exclusions.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.contains(bundle)
    }
    private func prune() {
        let cutoff = Date().addingTimeInterval(-300)
        let before = queue.events.count
        queue.events.removeAll { date($0.occurredAt).map { $0 < cutoff } ?? true }
        if queue.events.count > 512 { queue.events.removeFirst(queue.events.count - 512) }
        dropped += before - queue.events.count
        queue.history = Array((queue.history ?? []).filter {
            date($0.occurredAt).map { $0 >= cutoff } ?? false
        }.suffix(512))
        saveQueue()
    }
    private func saveQueue() {
        queue.origin = connectedOrigin
        queued = queue.events.count
        do {
            let file = storage.appendingPathComponent("queue.json")
            try JSONEncoder().encode(queue).write(to: file, options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
            publishContext()
        } catch { collecting = false; self.error = "Capture paused: the private queue could not be saved." }
    }
    private func publishContext() {
        let file = storage.appendingPathComponent("context.json")
        guard let origin = connectedOrigin, let credential,
              connectionState != .needsPairing,
              let snapshot = localContext(origin: origin, deviceID: credential.deviceID, context: context,
                generation: queue.generation, history: queue.history ?? [], collecting: collecting) else {
            try? FileManager.default.removeItem(at: file)
            return
        }
        do { try privateWrite(snapshot, to: file) }
        catch { self.error = "Frag context could not be saved locally." }
    }
    func pair() async {
        guard !busy else { return }
        busy = true; error = ""; pairingError = ""
        defer { busy = false; pairingCode = "" }
        var exchanged = false
        do {
            let origin = try serverOrigin(originInput)
            guard !pairingCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                pairingError = "Paste a fresh pairing code from Basalisk → Devices."; return
            }
            let body = try JSONSerialization.data(withJSONObject: ["code": pairingCode.trimmingCharacters(in: .whitespacesAndNewlines), "audience": "basalisk"])
            let response: PairResponse = try await transport.call(origin, path: "/auth/pair/exchange", method: "POST", body: body)
            guard response.token_type == "Bearer" else { throw CompanionError.response(0) }
            exchanged = true
            let saved = Credential(token: response.token, deviceID: response.device.id, expiresAt: response.device.expires_at)
            try Keychain.save(saved, origin: origin)
            collecting = false; context = nil; connectionEpoch = UUID()
            credential = saved; connectedOrigin = origin; originInput = origin
            queue = QueueState(); saveQueue()
            connectionState = .checking; pairingCompletedAt = Date()
            lastCheckedAt = nil; lastSentAt = nil
            preferences.set(origin, forKey: "serverOrigin")
            status = "Paired. Start a work session in Basalisk, then enable capture here."
            await tick()
        } catch {
            if exchanged {
                pairingError = "Basalisk accepted the code, but this Mac could not save its credential in Keychain. Revoke the unused device in Basalisk → Devices, then create a fresh code and try again."
            } else if case CompanionError.response(401) = error {
                pairingError = "That code is expired, already used, or invalid. Create a new code in Basalisk → Devices and paste it here."
            } else if error is URLError {
                pairingError = "Could not reach Basalisk. Check the server address and keep your SSH tunnel open, then try a fresh pairing code."
            } else {
                pairingError = error.localizedDescription
            }
        }
    }
    func refreshConnection() async {
        guard connectedOrigin != nil, !busy else { return }
        connectionState = .checking
        await tick()
    }
    func disconnect() {
        collecting = false; context = nil; connectionEpoch = UUID()
        if let origin = connectedOrigin {
            do { try Keychain.remove(origin) } catch { self.error = error.localizedDescription; return }
        }
        credential = nil; connectedOrigin = nil
        connectionState = .unpaired; pairingError = ""; pairingCompletedAt = nil
        lastCheckedAt = nil; lastSentAt = nil; sessionTitle = "No active work session"
        hasActiveSession = false
        queue = QueueState(); saveQueue()
        status = "Disconnected locally. Revoke this device in Basalisk to remove server access."
    }
    func toggleCapture() {
        guard connectedOrigin != nil, connectionState != .needsPairing, !busy else { return }
        collecting.toggle()
        if collecting {
            status = "Capture enabled for active work sessions."
            captureFrontmost()
            publishContext()
        } else {
            queue.events.removeAll(); queue.history = []; publishContext(); saveQueue()
            status = "Capture paused; unsent observations discarded."
        }
    }
    private func captureFrontmost() {
        guard observesWorkspace else { return }
        guard let app = NSWorkspace.shared.frontmostApplication else { return }
        record(kind: "app.activated", bundle: app.bundleIdentifier ?? "", name: app.localizedName ?? "")
    }
    func record(kind: String, bundle: String, name: String) {
        let now = Date()
        guard collecting, !excluded(bundle), let context, let session = context.current, session.status == "active",
              let asOf = date(context.asOf), now.timeIntervalSince(asOf) >= 0.01, now.timeIntervalSince(asOf) <= 30 else { return }
        // App/window contents and URLs are deliberately absent. The context is
        // the last server observation, not a claim of universal OS causation.
        let event = Observation(sequence: queue.nextSequence, occurredAt: stamp(now), kind: kind,
                                bundleId: bounded(bundle, bytes: 255), appName: bounded(name, bytes: 200),
                                activitySessionId: session.id, contextId: session.context.id, contextAsOf: context.asOf)
        queue.nextSequence += 1; queue.events.append(event)
        queue.history = (queue.history ?? []) + [event]
        prune()
        Task { await flush() }
    }
    private func tick() async {
        prune()
        guard let origin = connectedOrigin, var credential else { return }
        let epoch = connectionEpoch
        await syncCaptures()
        guard epoch == connectionEpoch else { return }
        do {
            if let expires = date(credential.expiresAt), expires.timeIntervalSinceNow < 7 * 86400 {
                let renewed: DeviceResponse = try await transport.call(origin, path: "/auth/renew", method: "POST", token: credential.token, body: Data("{}".utf8))
                guard epoch == connectionEpoch else { return }
                credential.expiresAt = renewed.expires_at
                try Keychain.save(credential, origin: origin); self.credential = credential
            }
            let next: ContextResponse = try await transport.call(origin, path: "/companion/context", token: credential.token)
            guard epoch == connectionEpoch else { return }
            connectionState = .connected; lastCheckedAt = Date()
            let previousID = context?.current?.context.id
            let previousStatus = context?.current?.status
            context = next; sessionTitle = next.current?.title ?? "No active work session"
            hasActiveSession = next.current?.status == "active"
            if next.current?.status != "active" { status = "Waiting for an active work session." }
            else { status = collecting ? "Capturing app focus. Connected to Basalisk." : "Paired. Capture is paused." }
            if hasActiveSession {
                // Preserve the device clock. A small host clock difference can
                // put this server snapshot just ahead of our current time.
                let delay = max(0.02, (date(next.asOf)?.timeIntervalSinceNow ?? 0) + 0.02)
                if delay <= 30 {
                    try? await Task.sleep(for: .seconds(delay))
                    guard epoch == connectionEpoch else { return }
                    if next.current?.context.id != previousID || previousStatus != "active" { captureFrontmost() }
                }
            }
            publishContext()
            await flush()
            await sendArtifactReceipts()
        } catch {
            guard epoch == connectionEpoch else { return }
            connectionState = .offline
            status = "Offline or unavailable. Context expires after 30 seconds; queued history expires after 5 minutes."
            if case CompanionError.response(401) = error {
                collecting = false; context = nil; connectionState = .needsPairing
                status = "Access expired or was revoked. Pair this Mac again with a fresh code."
                publishContext()
            }
        }
    }
    private func sendArtifactReceipts() async {
        guard let origin = connectedOrigin, let credential else { return }
        let epoch = connectionEpoch
        let directory = artifactDirectory
        guard let files = try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: [.fileSizeKey, .isRegularFileKey]) else { return }
        var attempts = 0
        for file in files.filter({ $0.pathExtension == "json" }).sorted(by: { $0.lastPathComponent < $1.lastPathComponent }) {
            guard let values = try? file.resourceValues(forKeys: [.fileSizeKey, .isRegularFileKey, .isSymbolicLinkKey]),
                  values.isRegularFile == true, values.isSymbolicLink != true, (values.fileSize ?? 0) < 16384,
                  let data = try? Data(contentsOf: file), let receipt = try? JSONDecoder().decode(ArtifactReceipt.self, from: data),
                  receipt.draft.origin == origin, receipt.draft.deviceID == credential.deviceID else { continue }
            if let saved = date(receipt.savedAt), saved < Date().addingTimeInterval(-30 * 86400) {
                try? FileManager.default.removeItem(at: file); continue
            }
            attempts += 1
            if attempts > 20 { break }
            do {
                struct Ack: Decodable { let saved: Bool }
                let ack: Ack = try await transport.call(origin, path: "/companion/artifacts", method: "POST", token: credential.token, body: data)
                guard epoch == connectionEpoch else { return }
                if ack.saved { try FileManager.default.removeItem(at: file) }
            } catch CompanionError.response(let code) where code == 400 || code == 409 || code == 403 {
                try? FileManager.default.moveItem(at: file, to: file.appendingPathExtension("rejected"))
                self.error = "A Frag session link could not be accepted. The fragment is saved in Fragments Engine; its local receipt is retained for review."
            } catch { return } // Keep exact receipt for transient retry; never block Frag's save.
        }
    }
    private func flush() async {
        guard !flushing, collecting, let origin = connectedOrigin, let credential, !queue.events.isEmpty else { return }
        flushing = true
        defer { flushing = false }
        let epoch = connectionEpoch, generation = queue.generation
        let batch = Array(queue.events.prefix(10))
        do {
            let body = try JSONEncoder().encode(Batch(generation: generation, events: batch))
            let _: Receipt = try await transport.call(origin, path: "/companion/observations", method: "POST", token: credential.token, body: body)
            guard epoch == connectionEpoch, generation == queue.generation else { return }
            lastSentAt = Date()
            let sent = Set(batch.map(\.sequence))
            queue.events.removeAll { sent.contains($0.sequence) }; saveQueue()
        } catch {
            guard epoch == connectionEpoch else { return }
            if case CompanionError.response(401) = error {
                collecting = false; context = nil; connectionState = .needsPairing
                status = "Access expired or was revoked. Pair this Mac again with a fresh code."
            }
            // Keep exact generation/sequence/payload for retries. Bounded TTL
            // prevents a malformed or unavailable batch becoming an unbounded log.
        }
    }
    func handleCapture(_ data: Data) -> CaptureReply {
        guard captureAllowed else { return .failed("Allow Frag in Basalisk Companion → Fragment capture first.") }
        guard let outbox = captureOutbox else { return .failed("Capture storage is unavailable. Keep your draft.") }
        do {
            let request = try JSONDecoder().decode(CaptureRequest.self, from: data)
            if request.operation == "status", let id = request.captureID {
                return outbox.entries.first(where: { $0.id == id })?.reply ?? .failed("Capture not found. Keep your draft.")
            }
            guard request.operation == "submit", let note = request.submission else { return .failed("Unsupported capture operation.") }
            // Existing exact retries remain resolvable after a connection change.
            if let old = outbox.entries.first(where: { $0.id == note.captureID }) {
                return try outbox.accept(note, binding: old.binding)
            }
            guard let credential, let origin = connectedOrigin,
                  let expires = date(credential.expiresAt), expires > Date(),
                  let binding = captureBinding, binding.origin == origin, binding.deviceID == credential.deviceID else {
                return .failed("Pair with fragment permission and choose an FE connection in Basalisk → Devices. Keep your draft.")
            }
            let reply = try outbox.accept(note, binding: binding)
            captures = outbox.entries
            Task { await syncCaptures() }
            return reply
        } catch let problem as CaptureFailure { return .failed(problem.localizedDescription) }
        catch { return .failed("The note could not be stored safely. Keep your draft and check Companion.") }
    }
    func retryCaptures() async { await syncCaptures(retryAttention: true) }
    func forgetCapture(_ id: String) {
        do { try captureOutbox?.forgetCompleted(id); captures = captureOutbox?.entries ?? [] }
        catch { self.error = "Could not update the capture outbox." }
    }
    private func syncCaptures(retryAttention: Bool = false) async {
        guard captureAllowed, !syncingCaptures, let origin = connectedOrigin, let credential,
              let outbox = captureOutbox else { return }
        syncingCaptures = true
        defer { syncingCaptures = false; captures = outbox.entries }
        let epoch = connectionEpoch
        struct Connection: Decodable { let deviceId: String; let integrationId: String; let name: String }
        do {
            let binding: Connection = try await transport.call(origin, path: "/companion/fragment-connection", token: credential.token)
            guard epoch == connectionEpoch, binding.deviceId == credential.deviceID else { return }
            let next = CaptureBinding(origin: origin, deviceID: binding.deviceId, integrationID: binding.integrationId, name: binding.name)
            try privateWrite(next, to: storage.appendingPathComponent("capture-binding.json"))
            captureBinding = next; captureStatus = "Frag can queue notes for " + binding.name + ". Activity capture may stay paused."
        } catch {
            if case CompanionError.response(let status) = error, [401,403,409].contains(status) {
                captureBinding = nil
                try? FileManager.default.removeItem(at: storage.appendingPathComponent("capture-binding.json"))
                captureStatus = "Pair with fragment permission and choose an FE connection in Basalisk → Devices."
            } else { captureStatus = "Basalisk is unavailable. Queued notes are retained on this Mac." }
        }
        for old in outbox.entries where old.envelope != nil {
            guard epoch == connectionEpoch, captureAllowed else { return }
            var entry = old
            guard entry.binding.origin == origin, entry.binding.deviceID == credential.deviceID else {
                entry.state = "needs_attention"; entry.message = "Original pairing is unavailable. Note retained; copy it before changing or removing it."
                try? outbox.update(entry); continue
            }
            if entry.state == "needs_attention" && !retryAttention { continue }
            struct Status: Decodable { let captureID: String; let deviceID: String; let integrationID: String; let state: String; let code: String; let fragmentID: String? }
            do {
                let response: Status
                if entry.serverAccepted {
                    response = try await transport.call(origin, path: "/companion/fragments/" + entry.captureID, token: credential.token)
                } else {
                    response = try await transport.call(origin, path: "/companion/fragments", method: "POST", token: credential.token, body: JSONEncoder().encode(entry.envelope!))
                }
                guard response.captureID == entry.captureID, response.deviceID == entry.binding.deviceID, response.integrationID == entry.binding.integrationID else { throw CaptureFailure("Unexpected capture receipt.") }
                entry.serverAccepted = true
                switch response.state {
                case "saved":
                    guard let id = response.fragmentID, !id.isEmpty else { throw CaptureFailure("Save was not confirmed.") }
                    entry.state = "saved"; entry.message = "Saved to Fragments Engine"; entry.fragmentID = id; entry.envelope = nil
                case "discarded": entry.state = "discarded"; entry.message = "Discarded by owner in Basalisk"; entry.envelope = nil
                case "queued": entry.state = "queued"; entry.message = response.code == "delivery_unavailable" ? "Queued in Basalisk · FE delivery is not available yet" : "Queued in Basalisk · awaiting FE confirmation"
                default: entry.state = "needs_attention"; entry.message = "Basalisk reports a delivery problem. Review the fragment outbox."
                }
                try outbox.update(entry)
            } catch {
                if case CompanionError.response(let status) = error, [400,401,403,404,409,413].contains(status) {
                    entry.state = "needs_attention"; entry.message = "Basalisk could not accept or find this note (HTTP \(status)). Note retained; review its original connection."
                    try? outbox.update(entry)
                }
                // Lost ack or network failure: preserve exact request and retry.
                captureStatus = "Some notes are waiting. Keep the tunnel open, or review the outbox."
                return
            }
        }
    }

}

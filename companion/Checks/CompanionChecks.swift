import Foundation
import Darwin

final class OfflineObservations: URLProtocol {
    static let lock = NSLock()
    private static var failing = true
    static func recover() { lock.lock(); failing = false; lock.unlock() }
    override class func canInit(with request: URLRequest) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return failing && request.url?.path == "/companion/observations"
    }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
    }
    override func stopLoading() {}
}

@main struct CompanionChecks {
    static func main() async {
        var result: Int32 = 0
        do { try await run() }
        catch { fputs("Native checks failed: \(error)\n", stderr); result = 1 }
        if let path = ProcessInfo.processInfo.environment["BASALISK_CHECK_RESULT"] {
            try? Data("\(result)\n".utf8).write(to: URL(fileURLWithPath: path), options: .atomic)
        }
        exit(result)
    }
    static func run() async throws {
        let checks = CompanionChecks()
        try checks.testOriginBoundary()
        try checks.testKeychainRoundTripIsOriginBound()
        print("Origin boundary and native Keychain checks passed."); fflush(stdout)
        try checks.testTimestampAndMetadataBoundary()
        try checks.testLocalContextExpiryAndPrecedingApp()
        try await checks.testCaptureOutbox()
        try await checks.testNativeTransportAgainstDisposableBackend()
        print("Native checks passed: origin boundary, Keychain custody, timestamps, metadata, and any configured backend round trip.")
    }
    func testOriginBoundary() throws {
        try equal(try serverOrigin(" HTTP://127.0.0.1:18080/ "), "http://127.0.0.1:18080")
        try equal(try serverOrigin("https://example.test/"), "https://example.test")
        for invalid in ["http://100.64.0.1", "https://example.test/path", "https://example.test?key=value",
                        "https://example.test#fragment", "https://user@example.test", "ftp://localhost", "http://localhost:0"] {
            try rejects { try serverOrigin(invalid) }
        }
    }

    func testKeychainRoundTripIsOriginBound() throws {
        let origin = "https://\(UUID().uuidString.lowercased()).invalid"
        defer { try? Keychain.remove(origin) }
        let credential = Credential(token: UUID().uuidString, deviceID: "test-device", expiresAt: "2030-01-01T00:00:00Z")
        try absent(try Keychain.read(origin))
        try Keychain.save(credential, origin: origin)
        try equal(try Keychain.read(origin)?.token, credential.token)
        try absent(try Keychain.read(origin + ":444"))
        var renewed = credential
        renewed.expiresAt = "2030-02-01T00:00:00Z"
        try Keychain.save(renewed, origin: origin)
        try equal(try Keychain.read(origin)?.expiresAt, renewed.expiresAt)
        try Keychain.remove(origin)
        try absent(try Keychain.read(origin))
    }

    func testTimestampAndMetadataBoundary() throws {
        let source = try require(date("2026-10-18T12:34:56.123456789Z"))
        let roundTrip = try require(date(stamp(source)))
        try lessThan(abs(source.timeIntervalSince(roundTrip)), 0.002)
        try present(date("2026-10-18T12:34:56Z"))
        try absent(date("not-a-date"))
        try equal(bounded("A\n😀B", bytes: 5), "A😀")
    }

    func testLocalContextExpiryAndPrecedingApp() throws {
        let now = Date()
        let session = WorkSession(id: "s", title: "Test", status: "active", context: WorkContext(id: "c", project: "P", workstream: "W"))
        let context = ContextResponse(current: session, asOf: stamp(now.addingTimeInterval(-2)))
        func event(_ sequence: Int64, _ bundle: String, _ age: Double) -> Observation {
            Observation(sequence: sequence, occurredAt: stamp(now.addingTimeInterval(-age)), kind: "app.activated", bundleId: bundle,
                appName: bundle, activitySessionId: "s", contextId: "c", contextAsOf: context.asOf)
        }
        let history = [event(1, "old", 301), event(2, "editor", 1), event(3, "com.hollis-labs.frag", 0)]
        let snapshot = try require(localContext(origin: "http://localhost", deviceID: "d", context: context, generation: "g", history: history, collecting: true, now: now))
        try equal(snapshot.precedingApp, "editor")
        try equal(snapshot.historyRefs, ["d:g:2"])
        try absent(localContext(origin: "http://localhost", deviceID: "d", context: context, generation: "g", history: history, collecting: false, now: now))
        try absent(localContext(origin: "http://localhost", deviceID: "d", context: context, generation: "g", history: history, collecting: true, now: now.addingTimeInterval(31)))
    }

    @MainActor func testCaptureOutbox() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: dir) }
        let binding = CaptureBinding(origin: "http://localhost", deviceID: "device", integrationID: "integration", name: "Synthetic")
        let now = stamp(Date())
        let note = CaptureDraft(captureID: UUID().uuidString, content: "private test note", tags: [], draftAt: now, submittedAt: now, draft: nil, saveContext: nil)
        var queue = try CaptureOutbox(directory: dir)
        try equal(try queue.accept(note, binding: binding).state, "queued")
        queue = try CaptureOutbox(directory: dir)
        try equal(queue.entries.first?.envelope?.content, note.content)
        let changedBinding = CaptureBinding(origin: "https://other.test", deviceID: "new-device", integrationID: "new-integration", name: "Other")
        _ = try queue.accept(note, binding: changedBinding)
        try equal(queue.entries.first?.binding.deviceID, binding.deviceID, "Retry retargeted existing note")
        let edited = CaptureDraft(captureID: note.captureID, content: "changed", tags: [], draftAt: now, submittedAt: now, draft: nil, saveContext: nil)
        try rejects { try queue.accept(edited, binding: binding) }
        try queue.forgetCompleted(note.captureID)
        try equal(queue.entries.first?.envelope?.content, note.content, "Pending note silently discarded")
        let mode = try FileManager.default.attributesOfItem(atPath: dir.appendingPathComponent("captures.json").path)[.posixPermissions] as? NSNumber
        try equal(mode?.intValue, 0o600)
        try Data("corrupt".utf8).write(to: dir.appendingPathComponent("captures.json"))
        try rejects { try CaptureOutbox(directory: dir) }
        print("Capture outbox passed: durable ack, retry identity, fixed destination, private storage, fail-closed corruption.")
    }

    // Opt-in: this creates an owner in a NEW disposable backend only.
    // Never point BASALISK_TEST_ORIGIN at an existing user installation.
    @MainActor func testNativeTransportAgainstDisposableBackend() async throws {
        guard let raw = ProcessInfo.processInfo.environment["BASALISK_TEST_ORIGIN"], !raw.isEmpty else {
            print("Backend check skipped: set BASALISK_TEST_ORIGIN to a fresh disposable backend."); return
        }
        let origin = try serverOrigin(raw)
        let admin = URLSession(configuration: .ephemeral)
        defer { admin.invalidateAndCancel() }
        func request(_ path: String, method: String = "GET", body: [String: Any]? = nil) async throws -> Data {
            var request = URLRequest(url: URL(string: origin + path)!)
            request.httpMethod = method
            request.setValue("1", forHTTPHeaderField: "X-Basalisk-Request")
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            if let body { request.httpBody = try JSONSerialization.data(withJSONObject: body) }
            let (data, response) = try await admin.data(for: request)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                throw CompanionError.response((response as? HTTPURLResponse)?.statusCode ?? 0)
            }
            return data
        }
        struct Setup: Decodable { let setupRequired: Bool }
        let setup = try JSONDecoder().decode(Setup.self, from: await request("/api/setup"))
        guard setup.setupRequired else { throw CheckFailure.failed("Refusing to mutate an existing owner installation.") }
        _ = try await request("/api/setup", method: "POST", body: ["username": "native-test", "displayName": "Native test", "password": UUID().uuidString])
        _ = try await request("/api/activity/commands", method: "POST", body: ["requestId": UUID().uuidString, "action": "start", "title": "Native transport test", "project": "Synthetic"])
        struct Grant: Decodable { let code: String }
        // Exercise the real UI model with isolated preferences, queue and origin.
        // Never observe NSWorkspace or enable collection during these checks.
        let suite = "com.hollislabs.basalisk.checks.\(UUID().uuidString)"
        let preferences = try require(UserDefaults(suiteName: suite))
        preferences.set(origin, forKey: "serverOrigin")
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [OfflineObservations.self]
        let transportWithOutage = Transport(configuration: config)
        var model = CompanionModel(preferences: preferences, storage: storage, startServices: false, transport: transportWithOutage)
        defer {
            model.disconnect()
            preferences.removePersistentDomain(forName: suite)
            try? FileManager.default.removeItem(at: storage)
        }
        model.pairingCode = "invalid-test-code"
        await model.pair()
        try equal(model.connectionState, .unpaired)
        try absent(model.pairingCompletedAt)
        guard !model.pairingError.isEmpty, !model.busy else { throw CheckFailure.failed("Failed pairing has no actionable UI feedback") }
        let modelGrant = try JSONDecoder().decode(Grant.self, from: await request("/api/devices/grants", method: "POST", body: ["label": "UI state check", "allowFragments": true]))
        model.pairingCode = modelGrant.code
        await model.pair()
        try equal(model.connectionState, .connected)
        try present(model.pairingCompletedAt)
        try equal(model.pairingError, "")
        try equal(model.pairingCode, "")
        try equal(model.collecting, false)
        try equal(model.hasActiveSession, true)
        struct Integration: Decodable { let id: String }
        let integration = try JSONDecoder().decode(Integration.self, from: await request("/api/integrations", method: "POST", body: ["name": "Disposable FE", "provider": "fragments-engine", "baseUrl": "https://fe.example.test", "credential": "synthetic-unused-credential"]))
        let captureDevice = try require(Keychain.read(origin)?.deviceID)
        _ = try await request("/api/fragment-connections/" + captureDevice, method: "PUT", body: ["integrationId": integration.id])
        let captureID = UUID().uuidString
        let captureTime = stamp(Date())
        let captureBody = try JSONSerialization.data(withJSONObject: ["operation": "submit", "submission": ["captureID": captureID, "content": "Synthetic queued note", "tags": ["test"], "draftAt": captureTime, "submittedAt": captureTime]])
        try equal(model.handleCapture(captureBody).state, "rejected", "Local app approval was not required")
        model.captureAllowed = true
        await model.retryCaptures()
        try equal(model.handleCapture(captureBody).state, "queued")
        await model.retryCaptures()
        // handleCapture schedules an async sync too; wait for that exact work.
        for _ in 0..<30 where model.captures.first?.serverAccepted != true { try await Task.sleep(for: .milliseconds(100)) }
        try equal(model.captures.first?.serverAccepted, true)
        try equal(model.captures.first?.state, "queued", "Unconfirmed FE capture reported saved")
        try equal(model.collecting, false, "Note capture enabled activity collection")
        try equal(model.handleCapture(captureBody).captureID, captureID)
        let queueData = try await request("/api/fragment-submissions")
        struct CapturePage: Decodable { struct Item: Decodable { let sequence: Int; let captureID: String }; let items: [Item] }
        let storedCapture = try require(JSONDecoder().decode(CapturePage.self, from: queueData).items.first)
        try equal(storedCapture.captureID, captureID)
        _ = try await request("/api/fragment-submissions/" + String(storedCapture.sequence), method: "DELETE")
        await model.retryCaptures()
        try equal(model.captures.first?.state, "discarded")
        try absent(model.captures.first?.envelope)
        print("Native capture round trip passed: explicit local approval, device binding, durable queue, honest pending state, owner discard.")

        try await Task.sleep(for: .milliseconds(200))
        model.toggleCapture()
        model.record(kind: "app.activated", bundle: "test.bridge-check", name: "Synthetic editor")
        let bridge = storage.appendingPathComponent("context.json")
        let snapshot = try JSONDecoder().decode(LocalContext.self, from: Data(contentsOf: bridge))
        try equal(snapshot.precedingApp, "Synthetic editor")
        let draftAt = stamp(Date())
        try await Task.sleep(for: .milliseconds(200))
        try equal(model.queued, 1, "Failed delivery did not retain its observation")
        // Relaunch during an outage: the queue survives, capture returns paused,
        // then the same generation/sequence can be delivered after recovery.
        model = CompanionModel(preferences: preferences, storage: storage, startServices: false, transport: transportWithOutage)
        try equal(model.collecting, false)
        try equal(model.queued, 1)
        OfflineObservations.recover()
        model.toggleCapture()
        await model.refreshConnection()
        try equal(model.queued, 0, "Recovery did not flush the durable queue")
        // Acknowledging the observation must not erase bounded local history.
        let afterSend = try JSONDecoder().decode(LocalContext.self, from: Data(contentsOf: bridge))
        try equal(afterSend.historyRefs, snapshot.historyRefs)
        await model.refreshConnection()
        let heartbeat = try JSONDecoder().decode(LocalContext.self, from: Data(contentsOf: bridge))
        try equal(heartbeat.historyRefs, snapshot.historyRefs, "Idle refresh removed the valid context bridge")
        model.exclusions = "test.bridge-check"
        let excluded = try JSONDecoder().decode(LocalContext.self, from: Data(contentsOf: bridge))
        try absent(excluded.precedingApp)
        model.toggleCapture()
        try equal(FileManager.default.fileExists(atPath: bridge.path), false)
        let receipt = ArtifactReceipt(captureID: UUID().uuidString, draftAt: draftAt, savedAt: stamp(Date()), fragmentID: "synthetic-confirmed-fragment", fragmentRevisionID: nil,
            fragmentOrigin: "https://fragments.example.test", draft: snapshot, saveContext: nil)
        let pending = storage.appendingPathComponent("BasaliskLinks/receipt.json")
        try privateWrite(receipt, to: pending)
        await model.refreshConnection()
        try equal(FileManager.default.fileExists(atPath: pending.path), false, "Confirmed artifact receipt was not acknowledged")
        struct ArtifactPage: Decodable {
            struct Item: Decodable { let receipt: ArtifactReceipt }
            let items: [Item]
        }
        let links = try JSONDecoder().decode(ArtifactPage.self, from: await request("/api/activity/sessions/" + snapshot.sessionID + "/artifacts"))
        try equal(links.items.first?.receipt.fragmentID, receipt.fragmentID, "Receipt did not become a durable artifact association")
        let modelDevice = try require(Keychain.read(origin)?.deviceID)
        _ = try await request("/api/devices/" + modelDevice, method: "DELETE")
        await model.refreshConnection()
        try equal(model.connectionState, .needsPairing)
        try equal(model.collecting, false)
        print("Pairing UI model passed: invalid code, successful connection, explicit capture opt-in, revoked access."); fflush(stdout)
        let grant = try JSONDecoder().decode(Grant.self, from: await request("/api/devices/grants", method: "POST", body: ["label": "Disposable native test"]))
        let transport = Transport()
        let exchange: PairResponse = try await transport.call(origin, path: "/auth/pair/exchange", method: "POST",
            body: JSONSerialization.data(withJSONObject: ["code": grant.code, "audience": "basalisk"]))
        try equal(exchange.token_type, "Bearer")
        let context: ContextResponse = try await transport.call(origin, path: "/companion/context", token: exchange.token)
        let session = try require(context.current)
        let asOf = try require(date(context.asOf))
        print("Native pairing passed; device minus server context time: \(Date().timeIntervalSince(asOf)) seconds."); fflush(stdout)
        // Host clocks need not agree to a millisecond. Wait for a fresh event
        // whose real device timestamp follows the server's context timestamp.
        let delay = max(0.1, asOf.timeIntervalSinceNow + 0.1)
        guard delay < 5 else { throw CheckFailure.failed("Host clock offset exceeds native check tolerance") }
        try await Task.sleep(for: .seconds(delay))
        let event = Observation(sequence: 1, occurredAt: stamp(Date()), kind: "app.activated", bundleId: "test.synthetic", appName: "Synthetic native test",
            activitySessionId: session.id, contextId: session.context.id, contextAsOf: context.asOf)
        let body = try JSONEncoder().encode(Batch(generation: UUID().uuidString, events: [event]))
        for _ in 0..<2 {
            let receipt: Receipt = try await transport.call(origin, path: "/companion/observations", method: "POST", token: exchange.token, body: body)
            try equal(receipt.accepted, 1)
        }
        struct Stored: Decodable { let contextId: String; let bundleId: String }
        let stored = try JSONDecoder().decode([Stored].self, from: await request("/api/observations"))
        try equal(stored.filter { $0.bundleId == "test.synthetic" }.count, 1, "An identical retry must not duplicate history")
        try equal(stored.first?.contextId, session.context.id)
        let renewed: DeviceResponse = try await transport.call(origin, path: "/auth/renew", method: "POST", token: exchange.token, body: Data("{}".utf8))
        try equal(renewed.id, exchange.device.id)
        _ = try await request("/api/devices/" + exchange.device.id, method: "DELETE")
        do {
            let _: ContextResponse = try await transport.call(origin, path: "/companion/context", token: exchange.token)
            throw CheckFailure.failed("Revoked credential was accepted")
        } catch CompanionError.response(let status) { try equal(status, 401) }
    }
}

private enum CheckFailure: Error { case failed(String) }
private func equal<T: Equatable>(_ actual: T, _ expected: T, _ message: String = "Equality check failed") throws {
    guard actual == expected else { throw CheckFailure.failed(message) }
}
private func absent<T>(_ value: T?) throws {
    guard value == nil else { throw CheckFailure.failed("Expected absent value") }
}
private func present<T>(_ value: T?) throws { _ = try require(value) }
private func require<T>(_ value: T?) throws -> T {
    guard let value else { throw CheckFailure.failed("Expected present value") }; return value
}
private func lessThan(_ actual: Double, _ maximum: Double) throws {
    guard actual < maximum else { throw CheckFailure.failed("Timestamp exceeded tolerance") }
}
private func rejects<T>(_ operation: () throws -> T) throws {
    do { _ = try operation() } catch { return }
    throw CheckFailure.failed("Invalid origin accepted")
}

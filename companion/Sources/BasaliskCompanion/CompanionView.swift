import SwiftUI

struct CompanionView: View {
    @ObservedObject var model: CompanionModel
    @State private var changeConnection = false
    @State private var confirmDisconnect = false

    private var showPairing: Bool {
        model.connectedOrigin == nil || model.connectionState == .needsPairing || changeConnection
    }
    private var connectionLabel: String {
        if model.busy { return "Pairing…" }
        switch model.connectionState {
        case .unpaired: return "Not paired"
        case .checking: return "Checking connection…"
        case .connected: return "Connected"
        case .offline: return "Paired · offline"
        case .needsPairing: return "Pair again"
        }
    }
    private var connectionColor: Color {
        switch model.connectionState {
        case .connected: return .green
        case .offline, .needsPairing: return .orange
        default: return .secondary
        }
    }
    private var collectionLabel: String {
        if !model.collecting { return "Paused" }
        if model.connectionState != .connected { return "Waiting for connection" }
        return model.hasActiveSession ? "Capturing" : "Waiting for a session"
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                HStack(spacing: 14) {
                    Image(systemName: "circle.dotted.circle").font(.system(size: 32)).foregroundStyle(Color.accentColor)
                        .frame(width: 54, height: 54).background(Color.accentColor.opacity(0.1), in: RoundedRectangle(cornerRadius: 14))
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Basalisk companion").font(.title2.bold())
                        Text("Connect your Mac to your work sessions.").foregroundStyle(.secondary)
                    }
                    Spacer()
                }
                card {
                    HStack {
                        Text("Connection").font(.headline)
                        Spacer()
                        Label(connectionLabel, systemImage: model.connectionState == .connected ? "checkmark.circle.fill" : "network")
                            .font(.callout.weight(.medium)).foregroundStyle(connectionColor)
                            .padding(.horizontal, 10).padding(.vertical, 6)
                            .background(connectionColor.opacity(0.1), in: Capsule())
                    }
                    if let origin = model.connectedOrigin {
                        Text(origin).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                        if model.connectionState == .connected {
                            Label("This Mac is paired and can reach Basalisk.", systemImage: "checkmark.shield.fill")
                                .foregroundStyle(.green)
                        } else if model.connectionState == .offline {
                            notice("Your pairing is saved", "Keep the SSH tunnel open. The companion will reconnect automatically.", symbol: "wifi.exclamationmark", color: .orange)
                        } else if model.connectionState == .needsPairing {
                            notice("Access is no longer valid", "Create a fresh code in Basalisk → Devices, then pair again below.", symbol: "exclamationmark.triangle.fill", color: .orange)
                        }
                        if let checked = model.lastCheckedAt {
                            HStack(spacing: 4) { Text("Last connected"); Text(checked, style: .time) }
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    if showPairing {
                        Text("Open Basalisk → Devices and create a one-use code. Codes expire after five minutes.")
                            .foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        field("Basalisk server", hint: "Use the same local tunnel address as your browser.") {
                            TextField("http://127.0.0.1:18080", text: $model.originInput)
                                .accessibilityLabel("Basalisk server")
                        }
                        field("Pairing code", hint: "Paste the fresh code from the Devices page.") {
                            SecureField("Paste one-use pairing code", text: $model.pairingCode)
                                .accessibilityLabel("Pairing code")
                                .onSubmit { Task { await model.pair() } }
                        }
                        if !model.pairingError.isEmpty {
                            notice("Pairing didn’t finish", model.pairingError, symbol: "exclamationmark.circle.fill", color: .red)
                        }
                        HStack(spacing: 12) {
                            Button { Task { await model.pair() } } label: {
                                HStack(spacing: 8) {
                                    if model.busy { ProgressView().controlSize(.small) }
                                    Text(model.busy ? "Pairing Mac…" : "Pair this Mac")
                                }.frame(minWidth: 120)
                            }
                            .buttonStyle(.borderedProminent).controlSize(.large)
                            .disabled(model.busy || model.pairingCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                            if changeConnection {
                                Button("Cancel") { changeConnection = false; model.pairingCode = ""; model.pairingError = "" }
                                    .disabled(model.busy)
                            }
                        }
                    } else {
                        if model.pairingCompletedAt != nil {
                            notice("Mac paired successfully", "You’re ready. Start capture below when you want to record app activity.", symbol: "checkmark.circle.fill", color: .green)
                        }
                        HStack {
                            Button("Change connection") { changeConnection = true }
                            Spacer()
                            Button("Disconnect…", role: .destructive) { confirmDisconnect = true }
                        }.disabled(model.busy)
                    }
                }
                if !model.error.isEmpty {
                    notice("Something needs attention", model.error, symbol: "exclamationmark.triangle.fill", color: .red)
                }
                card {
                    HStack {
                        Text("Activity capture").font(.headline)
                        Spacer()
                        Label(collectionLabel, systemImage: model.collecting ? "record.circle" : "pause.circle")
                            .foregroundStyle(model.collecting ? Color.accentColor : .secondary)
                    }
                    Text(model.sessionTitle).font(.title3.weight(.medium))
                    Text(model.connectedOrigin == nil ? "Pair this Mac first. Capture stays off until you start it." : model.status)
                        .foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    Button {
                        model.toggleCapture()
                    } label: {
                        Label(model.collecting ? "Pause capture" : "Start capture", systemImage: model.collecting ? "pause.fill" : "play.fill")
                            .frame(minWidth: 120)
                    }
                    .buttonStyle(.borderedProminent).controlSize(.large)
                    .disabled(model.connectedOrigin == nil || model.connectionState == .needsPairing || model.busy)
                    HStack(spacing: 20) {
                        Label("\(model.queued) waiting to send", systemImage: "arrow.up.circle")
                        if let sent = model.lastSentAt {
                            HStack(spacing: 4) { Text("Last sent"); Text(sent, style: .time) }
                        }
                    }.font(.caption).foregroundStyle(.secondary)
                    if model.dropped > 0 {
                        Text("\(model.dropped) expired or dropped this launch").font(.caption).foregroundStyle(.secondary)
                    }
                }
                card {
                    Text("Fragment capture").font(.headline)
                    Toggle("Allow signed Frag to queue notes through this companion", isOn: $model.captureAllowed)
                    Text(model.captureStatus).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    Text("Notes stay in a private outbox until FE confirms delivery. Pausing activity capture does not pause note delivery.").font(.caption).foregroundStyle(.secondary)
                    Button("Check connection and retry") { Task { await model.retryCaptures() } }
                        .disabled(!model.captureAllowed)
                    ForEach(model.captures) { entry in
                        VStack(alignment: .leading, spacing: 6) {
                            Text(entry.message).font(.callout.weight(.medium))
                            Text(entry.captureID).font(.caption.monospaced()).foregroundStyle(.secondary)
                            if let note = entry.envelope {
                                DisclosureGroup("Review note") {
                                    Text(note.content).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
                                    Button("Copy note") { NSPasteboard.general.clearContents(); NSPasteboard.general.setString(note.content, forType: .string) }
                                }
                            } else {
                                Button("Dismiss receipt") { model.forgetCapture(entry.id) }
                            }
                        }.padding(.vertical, 8)
                    }
                }
                card {
                    Label("Your collection controls", systemImage: "hand.raised").font(.headline)
                    Text("Records app names and sleep/wake events during active work sessions. No window text, URLs, clipboard, keystrokes or screenshots. History expires after five minutes.")
                        .foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    field("Excluded apps", hint: "App bundle IDs, separated by commas. Changes apply immediately.") {
                        TextField("com.example.private-app, com.example.other-app", text: $model.exclusions)
                            .accessibilityLabel("Excluded app bundle IDs")
                    }
                    Text("Pausing discards unsent activity. Closing this window keeps the companion running; use Quit to exit.")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
            }.padding(24).frame(maxWidth: 760)
                .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .onChange(of: model.pairingCompletedAt) { _, value in if value != nil { changeConnection = false } }
        .confirmationDialog("Disconnect this Mac?", isPresented: $confirmDisconnect) {
            Button("Disconnect", role: .destructive) { model.disconnect() }
        } message: {
            Text("Capture stops and unsent activity is discarded. To revoke server access too, remove this device in Basalisk → Devices.")
        }
    }

    private func card<Content: View>(@ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 16, content: content)
            .frame(maxWidth: .infinity, alignment: .leading).padding(20)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 14))
            .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(Color.secondary.opacity(0.16)))
    }

    private func field<Content: View>(_ label: String, hint: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(label).font(.callout.weight(.semibold))
            content().textFieldStyle(.plain).font(.body)
                .padding(.horizontal, 12).frame(height: 42)
                .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 8))
                .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.secondary.opacity(0.4)))
                .disabled(model.busy)
            Text(hint).font(.caption).foregroundStyle(.secondary)
        }
    }

    private func notice(_ title: String, _ detail: String, symbol: String, color: Color) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: symbol).foregroundStyle(color).font(.title3)
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.callout.weight(.semibold))
                Text(detail).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
            }
            Spacer(minLength: 0)
        }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
            .background(color.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }
}


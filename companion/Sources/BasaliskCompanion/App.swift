import SwiftUI

@MainActor final class CompanionAppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.activate(ignoringOtherApps: true)
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        for window in sender.windows where window.canBecomeMain {
            window.deminiaturize(nil)
            window.makeKeyAndOrderFront(nil)
        }
        sender.activate(ignoringOtherApps: true)
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
}

@main @MainActor struct BasaliskCompanionApp: App {
    @NSApplicationDelegateAdaptor(CompanionAppDelegate.self) private var appDelegate
    @StateObject private var model = CompanionModel()
    @Environment(\.openWindow) private var openWindow
    var body: some Scene {
        Window("Basalisk companion", id: "settings") {
            CompanionView(model: model).frame(minWidth: 600, minHeight: 620)
        }
        .defaultSize(width: 680, height: 780)
        MenuBarExtra {
            Text(model.sessionTitle)
            Text(model.status)
            Divider()
            Button(model.collecting ? "Pause and discard queued activity" : "Start capture") { model.toggleCapture() }
                .disabled(model.connectedOrigin == nil)
            Button("Settings…") { openWindow(id: "settings"); NSApp.activate(ignoringOtherApps: true) }
            Divider()
            Button("Quit") { NSApp.terminate(nil) }
        } label: {
            Label("Basalisk", systemImage: model.collecting ? "circle.dotted.circle.fill" : "pause.circle")
                .labelStyle(.titleAndIcon)
        }
    }
}


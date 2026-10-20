import Foundation
import Darwin

@main @MainActor struct CaptureBridgeChecks {
    static func main() async {
        let args = CommandLine.arguments
        guard args.count == 4 else { exit(2) }
        if args[1] == "server" {
            do {
                let bridge = try CaptureBridge(serviceName: args[2]) { _ in Data("accepted".utf8) }
                try await Task.sleep(for: .seconds(55))
                withExtendedLifetime(bridge) {}
                exit(0)
            } catch { exit(3) }
        }
        do {
            let connection = NSXPCConnection(machServiceName: args[2])
            connection.setCodeSigningRequirement(try capturePeerRequirement(bundleID: "com.hollislabs.basalisk.companion"))
            connection.remoteObjectInterface = NSXPCInterface(with: CaptureServiceV1.self)
            connection.resume()
            let expected = args[3] == "accept"
            let proxy = connection.remoteObjectProxyWithErrorHandler { _ in
                connection.invalidate(); print(expected ? "Unexpected XPC rejection" : "Untrusted peer rejected"); exit(expected ? 1 : 0)
            } as! CaptureServiceV1
            proxy.request(Data("{}".utf8)) { data in
                connection.invalidate()
                let accepted = data == Data("accepted".utf8)
                print(accepted && expected ? "Signed peers connected" : "Unexpected XPC acceptance")
                exit(accepted && expected ? 0 : 1)
            }
            try await Task.sleep(for: .seconds(15))
            connection.invalidate(); print("XPC check timed out"); exit(4)
        } catch { print("XPC check setup failed"); exit(5) }
    }
}

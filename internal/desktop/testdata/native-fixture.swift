// Synthetic native acceptance target, never part of the AICE executable.
import AppKit

final class Fixture: NSObject, NSApplicationDelegate {
    let directory = URL(fileURLWithPath: CommandLine.arguments[1])
    let name = CommandLine.arguments[2]
    let sentinel = CommandLine.arguments[3] == "sentinel"
    var window: NSWindow!
    var input: NSTextField!
    var result: NSTextField!
    var timer: Timer?
    var commits = 0
    var focusLosses = 0
    var armed = false
    var ticks = 0

    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 100, y: 100, width: 500, height: 300),
                          styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        window.title = name
        window.isReleasedWhenClosed = false
        let view = window.contentView!
        let title = NSTextField(labelWithString: "Synthetic acceptance data only")
        title.frame = NSRect(x: 30, y: 240, width: 440, height: 25)
        view.addSubview(title)
        input = NSTextField(string: "AICE-314")
        input.frame = NSRect(x: 30, y: 180, width: 440, height: 30)
        input.setAccessibilityLabel("Task value")
        view.addSubview(input)
        let button = NSButton(title: "Commit", target: self, action: #selector(commit))
        button.frame = NSRect(x: 30, y: 125, width: 100, height: 32)
        view.addSubview(button)
        result = NSTextField(labelWithString: "Result: pending")
        result.frame = NSRect(x: 30, y: 65, width: 440, height: 30)
        view.addSubview(result)
        NotificationCenter.default.addObserver(self, selector: #selector(lostFocus),
                                               name: NSApplication.didResignActiveNotification, object: nil)
        window.orderFront(nil)
        if sentinel {
            if #available(macOS 14, *) { NSApp.activate() }
            else { NSApp.activate(ignoringOtherApps: true) }
            window.makeKeyAndOrderFront(nil)
            window.makeFirstResponder(input)
        }
        timer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in self?.sample() }
        sample()
    }

    @objc func commit() {
        commits += 1
        result.stringValue = "Result: " + input.stringValue
        sample()
    }

    @objc func lostFocus() {
        if armed { focusLosses += 1 }
    }

    func sample() {
        if !armed && FileManager.default.fileExists(atPath: directory.appendingPathComponent("arm").path) {
            armed = true
            focusLosses = 0
        }
        ticks += 1
        let state: [String: Any] = ["pid": ProcessInfo.processInfo.processIdentifier,
                                  "active": NSApp.isActive, "armed": armed,
                                  "visible": window.isVisible, "key": window.isKeyWindow,
                                  "front_pid": NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0,
                                  "front_is_login": NSWorkspace.shared.frontmostApplication?.bundleIdentifier == "com.apple.loginwindow",
                                  "activation_policy": NSRunningApplication.current.activationPolicy.rawValue,
                                  "focus_losses": focusLosses, "ticks": ticks,
                                  "value": input.stringValue, "result": result.stringValue,
                                  "commits": commits]
        do {
            let data = try JSONSerialization.data(withJSONObject: state, options: [.sortedKeys])
            try data.write(to: directory.appendingPathComponent("state.json"), options: .atomic)
        } catch {
            // A fixture that cannot report independent state cannot pass.
            NSApp.terminate(nil)
        }
    }
}

let app = NSApplication.shared
let fixture = Fixture()
app.setActivationPolicy(.regular)
app.delegate = fixture
app.run()

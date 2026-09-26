// Synthetic native acceptance target, never part of the AICE executable.
import AppKit

final class ScrollDocument: NSView {
    override var isFlipped: Bool { true }
}

final class Fixture: NSObject, NSApplicationDelegate {
    let directory = URL(fileURLWithPath: CommandLine.arguments[1])
    let name = CommandLine.arguments[2]
    let sentinel = CommandLine.arguments[3] == "sentinel"
    var window: NSWindow!
    var input: NSTextField!
    var result: NSTextField!
    var button: NSButton!
    var scroll: NSScrollView?
    var slider: NSSlider?
    var timer: Timer?
    var commits = 0
    var focusLosses = 0
    var armed = false
    var ticks = 0
    var resized = false

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
        button = NSButton(title: "Commit", target: self, action: #selector(commit))
        button.frame = NSRect(x: 30, y: 125, width: 100, height: 32)
        view.addSubview(button)
        result = NSTextField(labelWithString: "Result: pending")
        result.frame = NSRect(x: 30, y: 65, width: 440, height: 30)
        view.addSubview(result)
        if CommandLine.arguments[3] == "gestures" {
            window.setContentSize(NSSize(width: 900, height: 550))
            let scroller = NSScrollView(frame: NSRect(x: 530, y: 170, width: 320, height: 330))
            scroller.hasVerticalScroller = true
            let document = ScrollDocument(frame: NSRect(x: 0, y: 0, width: 300, height: 1800))
            for row in 0..<50 {
                let label = NSTextField(labelWithString: "Synthetic row \(row)")
                label.frame = NSRect(x: 12, y: row * 35, width: 260, height: 25)
                document.addSubview(label)
            }
            scroller.documentView = document
            view.addSubview(scroller)
            scroll = scroller
            let control = NSSlider(value: 0, minValue: 0, maxValue: 100, target: nil, action: nil)
            control.frame = NSRect(x: 530, y: 90, width: 320, height: 28)
            view.addSubview(control)
            slider = control
        }
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
        if !sentinel && !resized && FileManager.default.fileExists(atPath: directory.appendingPathComponent("resize").path) {
            resized = true
            window.setContentSize(NSSize(width: 900, height: 350))
        }
        if !armed && FileManager.default.fileExists(atPath: directory.appendingPathComponent("arm").path) {
            armed = true
            focusLosses = 0
        }
        ticks += 1
        let editor = input.currentEditor() as? NSTextView
        let selection = editor?.selectedRange()
        let buttonScreen = window.convertPoint(toScreen: button.convert(NSPoint(x: button.bounds.midX, y: button.bounds.midY), to: nil))
        var state: [String: Any] = ["pid": ProcessInfo.processInfo.processIdentifier,
                                  "active": NSApp.isActive, "armed": armed,
                                  "visible": window.isVisible, "key": window.isKeyWindow,
                                  "front_pid": NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0,
                                  "front_is_login": NSWorkspace.shared.frontmostApplication?.bundleIdentifier == "com.apple.loginwindow",
                                  "activation_policy": NSRunningApplication.current.activationPolicy.rawValue,
                                  "focus_losses": focusLosses, "ticks": ticks,
                                  "width": window.frame.width, "height": window.frame.height,
                                  "button_x": buttonScreen.x - window.frame.minX,
                                  "button_y": window.frame.maxY - buttonScreen.y,
                                  "value": editor?.string ?? input.stringValue, "result": result.stringValue,
                                  "selection_location": selection?.location ?? -1,
                                  "selection_length": selection?.length ?? -1,
                                  "commits": commits]
        if let scroller = scroll, let control = slider, let cell = control.cell as? NSSliderCell {
            let scrollPoint = capturePoint(scroller, NSPoint(x: scroller.bounds.midX, y: scroller.bounds.midY))
            let knob = cell.knobRect(flipped: control.isFlipped)
            let bar = cell.barRect(flipped: control.isFlipped)
            let from = capturePoint(control, NSPoint(x: knob.midX, y: knob.midY))
            let to = capturePoint(control, NSPoint(x: bar.minX + bar.width * 0.9, y: knob.midY))
            state["scroll_x"] = scrollPoint.x
            state["scroll_y"] = scrollPoint.y
            state["scroll_value"] = scroller.contentView.bounds.minY
            state["slider_value"] = control.doubleValue
            state["drag_from_x"] = from.x
            state["drag_from_y"] = from.y
            state["drag_to_x"] = to.x
            state["drag_to_y"] = to.y
        }
        do {
            let data = try JSONSerialization.data(withJSONObject: state, options: [.sortedKeys])
            try data.write(to: directory.appendingPathComponent("state.json"), options: .atomic)
        } catch {
            // A fixture that cannot report independent state cannot pass.
            NSApp.terminate(nil)
        }
    }

    func capturePoint(_ view: NSView, _ point: NSPoint) -> NSPoint {
        let screen = window.convertPoint(toScreen: view.convert(point, to: nil))
        return NSPoint(x: screen.x - window.frame.minX, y: window.frame.maxY - screen.y)
    }
}

let app = NSApplication.shared
let fixture = Fixture()
app.setActivationPolicy(.regular)
app.delegate = fixture
app.run()

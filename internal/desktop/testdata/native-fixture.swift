// Synthetic native acceptance target, never part of the AICE executable.
import AppKit

// LaunchServices supplies no task arguments. The cold-launch gate puts only
// its synthetic fixture settings in the temporary bundle's Info.plist.
func fixtureArgument(_ index: Int, _ key: String, default fallback: String? = nil) -> String {
    if CommandLine.arguments.count >= 4 { return CommandLine.arguments[index] }
    guard let value = Bundle.main.object(forInfoDictionaryKey: key) as? String ?? fallback else {
        fatalError("Missing synthetic fixture configuration")
    }
    return value
}

final class ScrollDocument: NSView {
    override var isFlipped: Bool { true }
}

// Counts actual AppKit mouse events without an AXPress implementation or a
// context menu. This isolates button/count delivery from menu behavior.
final class PointerSurface: NSView {
    var leftDowns = 0
    var leftUps = 0
    var rightDowns = 0
    var rightUps = 0
    var maxClickCount = 0
    var invalidEvents = 0
    var lastDistance = 0.0

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
    override func mouseDown(with event: NSEvent) { leftDowns += 1; record(event, button: 0) }
    override func mouseUp(with event: NSEvent) { leftUps += 1; record(event, button: 0) }
    override func rightMouseDown(with event: NSEvent) { rightDowns += 1; record(event, button: 1) }
    override func rightMouseUp(with event: NSEvent) { rightUps += 1; record(event, button: 1) }

    func record(_ event: NSEvent, button: Int) {
        let point = convert(event.locationInWindow, from: nil)
        let modifiers: NSEvent.ModifierFlags = [.command, .control, .option, .shift, .function]
        if event.windowNumber != window?.windowNumber || event.buttonNumber != button ||
           !bounds.contains(point) || !event.modifierFlags.intersection(modifiers).isEmpty {
            invalidEvents += 1
        }
        maxClickCount = max(maxClickCount, event.clickCount)
        lastDistance = hypot(point.x - bounds.midX, point.y - bounds.midY)
    }

    override func draw(_ dirtyRect: NSRect) {
        NSColor.systemTeal.setFill()
        bounds.fill()
        ("Pointer target" as NSString).draw(at: NSPoint(x: 12, y: 24),
            withAttributes: [.font: NSFont.systemFont(ofSize: 17), .foregroundColor: NSColor.white])
    }
}

final class Fixture: NSObject, NSApplicationDelegate {
    let directory = URL(fileURLWithPath: fixtureArgument(1, "AICEFixtureDirectory"))
    let name = fixtureArgument(2, "AICEFixtureName")
    let mode = fixtureArgument(3, "AICEFixtureMode", default: "target")
    var sentinel: Bool { mode == "sentinel" }
    var window: NSWindow!
    var input: NSTextField!
    var result: NSTextField!
    var button: NSButton!
    var scroll: NSScrollView?
    var slider: NSSlider?
    var pointer: PointerSurface?
    var timer: Timer?
    var commits = 0
    var focusLosses = 0
    var armed = false
    var ticks = 0
    var resized = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        if Bundle.main.object(forInfoDictionaryKey: "AICEFixtureDirectory") != nil {
            let marker = "launched-\(ProcessInfo.processInfo.processIdentifier)"
            try! Data().write(to: directory.appendingPathComponent(marker), options: .atomic)
        }
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
        if mode == "pointer" {
            let surface = PointerSurface(frame: NSRect(x: 240, y: 110, width: 220, height: 60))
            view.addSubview(surface)
            pointer = surface
        }
        if mode == "gestures" {
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
        if FileManager.default.fileExists(atPath: directory.appendingPathComponent("quit").path) {
            NSApp.terminate(nil)
            return
        }
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
        if let surface = pointer {
            let point = capturePoint(surface, NSPoint(x: surface.bounds.midX, y: surface.bounds.midY))
            state["pointer_x"] = point.x
            state["pointer_y"] = point.y
            state["left_downs"] = surface.leftDowns
            state["left_ups"] = surface.leftUps
            state["right_downs"] = surface.rightDowns
            state["right_ups"] = surface.rightUps
            state["max_click_count"] = surface.maxClickCount
            state["invalid_pointer_events"] = surface.invalidEvents
            state["pointer_distance"] = surface.lastDistance
        }
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

    func applicationWillTerminate(_ notification: Notification) {
        let marker = "terminated-\(ProcessInfo.processInfo.processIdentifier)"
        try? Data().write(to: directory.appendingPathComponent(marker), options: .atomic)
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

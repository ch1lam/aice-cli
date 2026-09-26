// Synthetic WebKit acceptance target. No remote content or user browser profile.
import AppKit
import WebKit

final class WebFixture: NSObject, NSApplicationDelegate, WKScriptMessageHandler {
    let directory = URL(fileURLWithPath: CommandLine.arguments[1])
    let name = CommandLine.arguments[2]
    var window: NSWindow!
    var web: WKWebView!
    var ticks = 0

    func applicationDidFinishLaunching(_ notification: Notification) {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.userContentController.add(self, name: "fixtureState")
        web = WKWebView(frame: NSRect(x: 0, y: 0, width: 600, height: 360), configuration: configuration)
        window = NSWindow(contentRect: web.frame, styleMask: [.titled, .closable],
                          backing: .buffered, defer: false)
        window.title = name
        window.isReleasedWhenClosed = false
        window.contentView = web
        window.orderFront(nil)
        // This page is the application under test. Its event handlers only
        // report real DOM state; the harness never calls JS to perform input.
        web.loadHTMLString("""
        <!doctype html><html lang="en"><head><meta charset="utf-8">
        <meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action 'none'">
        <title>Synthetic WebKit form</title>
        <style>body{font:18px system-ui;margin:30px}input{display:block;width:500px;margin:12px 0;padding:8px;font:inherit}button{font:inherit;padding:8px 20px}output{display:block;margin-top:24px}</style>
        </head><body><p>Synthetic acceptance data only — WebKit</p>
        <label for="value">Task value</label><input id="value" value="" autocomplete="off">
        <button id="commit" type="button">Commit</button><output id="result">Result: pending</output>
        <script>
        const field=document.getElementById('value'), result=document.getElementById('result');
        let commits=0;
        function report(){window.webkit.messageHandlers.fixtureState.postMessage({value:field.value,result:result.textContent,commits});}
        document.getElementById('commit').addEventListener('click',()=>{commits++;result.textContent='Result: '+field.value;report();});
        field.addEventListener('input',report);field.addEventListener('change',report);
        setInterval(report,50);report();
        </script></body></html>
        """, baseURL: nil)
    }

    func userContentController(_ userContentController: WKUserContentController,
                               didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let body = message.body as? [String: Any],
              let value = body["value"] as? String, let result = body["result"] as? String,
              let commits = body["commits"] as? Int else { return }
        ticks += 1
        let state: [String: Any] = ["pid": ProcessInfo.processInfo.processIdentifier,
                                   "active": NSApp.isActive, "visible": window.isVisible,
                                   "key": window.isKeyWindow, "ticks": ticks,
                                   "front_pid": NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0,
                                   "front_is_login": NSWorkspace.shared.frontmostApplication?.bundleIdentifier == "com.apple.loginwindow",
                                   "activation_policy": NSRunningApplication.current.activationPolicy.rawValue,
                                   "value": value, "result": result, "commits": commits]
        do {
            let data = try JSONSerialization.data(withJSONObject: state, options: [.sortedKeys])
            try data.write(to: directory.appendingPathComponent("state.json"), options: .atomic)
        } catch {
            NSApp.terminate(nil)
        }
    }
}

let app = NSApplication.shared
let fixture = WebFixture()
app.setActivationPolicy(.regular)
app.delegate = fixture
app.run()

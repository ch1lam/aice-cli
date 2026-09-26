"""Synthetic GTK targets for the opt-in isolated X11 test, never a runtime helper."""
import ctypes
import json
import os
from pathlib import Path
import sys

import gi

gi.require_version("Gtk", "3.0")
from gi.repository import GLib, Gtk

directory, title, mode = sys.argv[1:]
directory = Path(directory)
window = Gtk.Window(title=title)
window.set_default_size(420, 180)
window.move(40, 40)
box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
box.set_border_width(16)
entry = Gtk.Entry()
entry.get_accessible().set_name("Task value")
entry.set_text("" if mode == "sentinel" else "AICE-314")
button = Gtk.Button(label="Commit")
result = Gtk.Label(label="Result: pending")
for widget in (entry, button, result):
    box.pack_start(widget, True, True, 0)
window.add(box)
state = {"pid": os.getpid(), "armed": False, "focus_losses": 0,
         "keys_sent": 0, "commits": 0, "ticks": 0}


def commit(_):
    state["commits"] += 1
    result.set_text("Result: " + entry.get_text())


def focus_out(*_):
    if state["armed"]:
        state["focus_losses"] += 1
    return False


button.connect("clicked", commit)
window.connect("focus-out-event", focus_out)
window.connect("destroy", Gtk.main_quit)
window.show_all()
if mode == "input":
    entry.grab_focus()

# XTest sends ordinary core keyboard events to this container's isolated display.
# It is test input to the sentinel, never an alternative Cua action backend.
display = None
if mode == "sentinel":
    x11 = ctypes.CDLL("libX11.so.6")
    xtst = ctypes.CDLL("libXtst.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XKeysymToKeycode.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
    x11.XKeysymToKeycode.restype = ctypes.c_uint
    x11.XFlush.argtypes = [ctypes.c_void_p]
    xtst.XTestFakeKeyEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
    display = x11.XOpenDisplay(None)
    if not display:
        raise RuntimeError("isolated X11 display unavailable")
    keycode = x11.XKeysymToKeycode(display, ord("a"))
    entry.grab_focus()
    window.present()


def tick():
    state["ticks"] += 1
    if (directory / "resize").exists():
        (directory / "resize").unlink()
        window.resize(900, 350)
    if (directory / "activate").exists():
        (directory / "activate").unlink()
        entry.grab_focus()
        window.present()
    if mode == "sentinel" and (directory / "arm").exists():
        state["armed"] = True
    if state["armed"] and not (directory / "stop-typing").exists():
        # Do not reacquire focus: input delivered to another window is a failure.
        xtst.XTestFakeKeyEvent(display, keycode, 1, 0)
        xtst.XTestFakeKeyEvent(display, keycode, 0, 0)
        x11.XFlush(display)
        state["keys_sent"] += 1
    state.update(active=window.is_active(), value=entry.get_text(), result=result.get_text())
    button_x, button_y = button.translate_coordinates(window, 0, 0)
    state.update(width=window.get_allocated_width(), height=window.get_allocated_height(),
                 button_x=button_x + button.get_allocated_width() / 2,
                 button_y=button_y + button.get_allocated_height() / 2,
                 selection=list(entry.get_selection_bounds()))
    temporary = directory / "state.tmp"
    temporary.write_text(json.dumps(state))
    temporary.replace(directory / "state.json")
    return True


GLib.timeout_add(100, tick)
Gtk.main()

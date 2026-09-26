#!/usr/bin/env python3
"""Exercise the real Swift-TUI runtime with isolated state in a disposable PTY."""
import codecs
import fcntl
import json
import os
import pathlib
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
import unicodedata
import uuid

class TerminalScreen:
    """Decode the cursor/erase operations emitted by the real terminal renderer."""
    def __init__(self, width, height):
        self.width, self.height = width, height
        self.rows = [[" "] * width for _ in range(height)]
        self.x = self.y = 0
        self.pending = ""
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")

    def feed(self, data):
        self.pending += self.decoder.decode(data)
        text, i = self.pending, 0
        while i < len(text):
            c = text[i]
            if c == "\x1b":
                if i + 1 == len(text):
                    break
                if text[i + 1] == "[":
                    end = i + 2
                    while end < len(text) and not ("@" <= text[end] <= "~"):
                        end += 1
                    if end == len(text):
                        break
                    raw, command = text[i + 2:end], text[end]
                    if not raw.startswith(("?", ">", "=")):
                        values = [int(v) if v.isdigit() else 0 for v in raw.split(";")]
                        n = values[0] or 1
                        if command in "Hf":
                            self.y = min(self.height - 1, n - 1)
                            self.x = min(self.width - 1, (values[1] or 1) - 1 if len(values) > 1 else 0)
                        elif command == "A": self.y = max(0, self.y - n)
                        elif command == "B": self.y = min(self.height - 1, self.y + n)
                        elif command == "C": self.x = min(self.width - 1, self.x + n)
                        elif command == "D": self.x = max(0, self.x - n)
                        elif command == "G": self.x = min(self.width - 1, n - 1)
                        elif command == "J" and values[0] in (2, 3):
                            self.rows = [[" "] * self.width for _ in range(self.height)]
                        elif command == "K":
                            start = 0 if values[0] in (1, 2) else self.x
                            stop = self.width if values[0] in (0, 2) else self.x + 1
                            self.rows[self.y][start:stop] = [" "] * (stop - start)
                    i = end + 1
                    continue
                if text[i + 1] == "]":
                    end = text.find("\x07", i + 2)
                    st = text.find("\x1b\\", i + 2)
                    if end < 0 and st < 0: break
                    i = end + 1 if end >= 0 and (st < 0 or end < st) else st + 2
                    continue
                i += 2
                continue
            if c == "\r": self.x = 0
            elif c == "\n": self.y = min(self.height - 1, self.y + 1)
            elif c == "\b": self.x = max(0, self.x - 1)
            elif c >= " " and c != "\x7f":
                if self.x >= self.width:
                    self.x = 0
                    self.y = min(self.height - 1, self.y + 1)
                self.rows[self.y][self.x] = c
                self.x += 2 if unicodedata.east_asian_width(c) in ("W", "F") else 1
            i += 1
        self.pending = text[i:]

    def text(self):
        return "\n".join("".join(row) for row in self.rows)


repository = pathlib.Path(__file__).resolve().parent.parent
executable = pathlib.Path(sys.argv[1]).resolve()
width, height = (map(int, sys.argv[2:4]) if len(sys.argv) == 4 else (120, 30))

with tempfile.TemporaryDirectory(prefix="lazyxcode-terminal-") as directory:
    subprocess.run(
        [sys.executable, str(repository / "scripts/create-smoke-project.py"), directory],
        check=True,
    )
    schemes = pathlib.Path(directory) / "Smoke.xcodeproj/xcshareddata/xcschemes"
    (schemes / "SmokeAlt.xcscheme").write_text((schemes / "Smoke.xcscheme").read_text())
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", height, width, 0, 0))
    original = termios.tcgetattr(slave)
    environment = dict(
        os.environ,
        TERM="xterm-256color",
        LANG="en_US.UTF-8",
        XDG_STATE_HOME=directory + "/state",
        XDG_CACHE_HOME=directory + "/cache",
    )
    for key in list(environment):
        if key.startswith("LAZYXCODE_ASC_"):
            del environment[key]
    process = subprocess.Popen(
        [str(executable)], cwd=directory, stdin=slave, stdout=slave,
        stderr=slave, env=environment, start_new_session=True,
    )
    transcript = bytearray()
    screen = TerminalScreen(width, height)
    simulator_name = "lazyxcode QA " + uuid.uuid4().hex[:8] if os.environ.get("LAZYXCODE_DEVICE_SMOKE") == "1" else None

    def wait_until(predicate, description, offset=0, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if len(transcript) > offset and predicate():
                return
            ready, _, _ = select.select([master], [], [], 0.1)
            if ready:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                transcript.extend(chunk)
                screen.feed(chunk)
            if process.poll() is not None:
                break
        raise AssertionError(f"Missing {description}:\n{screen.text()}")

    def wait_for(text, offset=0, timeout=30):
        wait_until(lambda: text.decode() in screen.text(), repr(text), offset, timeout)

    def build_value(label):
        rows = screen.text().splitlines()
        for index, row in enumerate(rows[:-1]):
            if row.split("│")[1:2] and row.split("│")[1].strip() == label:
                return rows[index + 1].split("│")[1].strip()
        return ""

    def send(keys, expect):
        offset = len(transcript)
        os.write(master, keys)
        wait_for(expect, offset)

    try:
        wait_for(b"[1] Build")
        os.write(master, b"j")
        wait_until(lambda: build_value("Target").startswith("› "), "target focus during startup", timeout=5)
        os.write(master, b"k")
        wait_until(lambda: build_value("Scheme").startswith("› "), "scheme focus during startup", timeout=5)
        wait_until(lambda: "".join(screen.rows[-2]).strip().startswith("Ready"), "discovery completion")
        # Keep one live renderer: fresh snapshots cannot catch stale view reuse.
        for scheme in ["SmokeAlt", "Smoke", "SmokeAlt"]:
            send(b"\r", b"Type to filter")
            send(scheme.encode(), scheme.encode())
            send(b"\r", b"Actions")
            wait_until(lambda: build_value("Scheme") == "› " + scheme, "updated scheme in Build", timeout=5)
            os.write(master, b"j")
            wait_until(lambda: build_value("Target").startswith("› ") and build_value("Scheme") == scheme,
                       "focus moving from Scheme to Target without a pane switch", timeout=5)
            os.write(master, b"k")
            wait_until(lambda: build_value("Scheme") == "› " + scheme and not build_value("Target").startswith("› "),
                       "focus moving from Target to Scheme without a pane switch", timeout=5)
        os.write(master, b"j")
        wait_until(lambda: build_value("Target").startswith("› "), "target focus", timeout=5)
        # Exercise the target picker and its asynchronous refresh before menus.
        send(b"\r", b"Destination")
        send(b"Simulator", "/ Simulator".encode())
        def selected_destination_name():
            for row in screen.text().splitlines():
                menu_width = min(76, width - 2)
                left = (width - menu_width) // 2
                menu_row = row[left:left + menu_width]
                if menu_row.startswith("│") and menu_row[1:-1].strip().startswith("› "):
                    return menu_row[1:-1].strip()[2:].split("  ")[0]
            return ""
        wait_until(lambda: bool(selected_destination_name()), "a simulator in the picker")
        chosen_destination = selected_destination_name()
        send(b"\r", b"Actions")
        wait_until(lambda: build_value("Target").startswith("› ")
                   and chosen_destination.startswith(build_value("Target")[2:].rstrip("…")),
                   f"selected simulator {chosen_destination!r} in Build without a pane switch", timeout=5)
        os.write(master, b"k")
        wait_until(lambda: build_value("Scheme") == "› SmokeAlt", "scheme focus after choosing a simulator", timeout=5)
        send(b"c", b"Keep cache")
        os.write(master, b"\x1b")
        wait_until(lambda: "Clear build cache?" not in screen.text(), "cache dialog to close")
        if simulator_name:
            send(b"4", b"[n] New simulator")
            send(b"n", b"New simulator")
            send(b"iOS", b"/ iOS")
            send(b"\r", b"Device model")
            send(b"iPhone", b"/ iPhone")
            send(b"\r", b"Name:")
            send(b"\x15" + simulator_name.encode(), simulator_name.encode())
            os.write(master, b"\r")
            wait_until(lambda: "New simulator · Name" not in screen.text()
                       and "\u203a " + simulator_name in screen.text(), "created simulator selected", timeout=60)
            send(b"1j\r", b"Destination")
            send(simulator_name.encode(), ("/ " + simulator_name).encode())
            wait_until(lambda: selected_destination_name() == simulator_name,
                       "created simulator available as a build target", timeout=60)
            os.write(master, b"\x1b")
            wait_until(lambda: "Destination" not in screen.text(), "target picker to close")
            print("PASS: simulator created through Devices and found in Target picker", flush=True)
        send(b":", b"Type to filter")
        send(b"help", b"help")
        send(b"\r", b"Navigation")
        # The renderer may reuse unchanged cells in the title. Check new body text.
        send(b"\x1b", b"Choose a scheme")
        # Resize the real runtime and exercise all full-width compact panes.
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 10, 44, 0, 0))
        screen = TerminalScreen(44, 10)
        os.kill(process.pid, signal.SIGWINCH)
        send(b"1", b"Scheme")
        send(b"2", b"No activities yet")
        send(b"3", b"Ready when you are")
        send(b"4", b"[n] New simulator")
        send(b"n", b"New simulator")
        os.write(master, b"\x1b")
        wait_until(lambda: "New simulator ·" not in screen.text(), "runtime picker to close")
        send(b"3", b"Ready when you are")
        send(b":", b"Type to filter")
        send(b"\x1b", b"Ready when you are")
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", height, width, 0, 0))
        screen = TerminalScreen(width, height)
        os.kill(process.pid, signal.SIGWINCH)
        send(b"m", b"Cloud")
        # Let the missing-credentials message render before quitting.
        wait_for(b"LAZYXCODE_ASC_")
        os.write(master, b"q")
        # Keep draining while it restores the screen; a full PTY blocks writes.
        deadline = time.monotonic() + 10
        while process.poll() is None and time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.1)
            if ready:
                transcript.extend(os.read(master, 65536))
        process.wait(timeout=1)
        assert process.returncode == 0, f"Exit status {process.returncode}"
        assert termios.tcgetattr(slave) == original, "Terminal settings were not restored"
        print("PASS: startup focus, scheme/target selection and focus redraws, cache confirmation, action search, help, compact resize and navigation, Cloud mode, quit, terminal restoration")
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        os.close(master)
        os.close(slave)
        if simulator_name:
            inventory = json.loads(subprocess.check_output(["xcrun", "simctl", "list", "devices", "--json"]))
            for devices in inventory["devices"].values():
                for device in devices:
                    if device["name"] == simulator_name:
                        subprocess.run(["xcrun", "simctl", "delete", device["udid"]], check=True)
            print("PASS: disposable manager simulator cleaned up")

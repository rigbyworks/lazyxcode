#!/usr/bin/env python3
"""Exercise the real Swift-TUI runtime with isolated state in a disposable PTY."""
import codecs
import fcntl
import os
import pathlib
import pty
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time
import unicodedata

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

with tempfile.TemporaryDirectory(prefix="lazyxcode-terminal-") as directory:
    subprocess.run(
        [sys.executable, str(repository / "scripts/create-smoke-project.py"), directory],
        check=True,
    )
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
    original = termios.tcgetattr(slave)
    environment = dict(
        os.environ,
        TERM="xterm-256color",
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
    screen = TerminalScreen(120, 30)

    def wait_for(text, offset=0, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if len(transcript) > offset and text.decode() in screen.text():
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
        raise AssertionError(f"Missing {text!r}:\n{screen.text()}")

    def send(keys, expect):
        offset = len(transcript)
        os.write(master, keys)
        wait_for(expect, offset)

    try:
        wait_for(b"[Local]  Cloud [1]")
        # Exercise the target picker and its asynchronous refresh before menus.
        send(b"j\r", b"Destination")
        send(b"\x1b", b"Actions")
        send(b"c", b"Keep cache")
        send(b"\x1b", b"Actions")
        send(b":", b"Actions")
        send(b"help", b"help")
        send(b"\r", b"Navigation")
        # The renderer may reuse unchanged cells in the title. Check new body text.
        send(b"\x1b", b"Choose a scheme")
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
        print("PASS: startup, target picker, cache confirmation, action search, help, Cloud mode, quit, terminal restoration")
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

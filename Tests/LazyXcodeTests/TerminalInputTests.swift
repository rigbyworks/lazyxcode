import SwiftTUI
import Testing

// Colour-scheme reports (DEC mode 2031) and the colour replies they prompt arrive on the same input as keys.
// None of their bytes may reach lazyxcode as a key press, wherever the terminal splits them across reads.
@Test(arguments: [
    "\u{1B}[?997;1n", "\u{1B}[?997;2n", "\u{1B}]11;rgb:f7f7/f7f7/f7f7\u{7}", "\u{1B}]10;rgb:0/0/0\u{1B}\\",
    "\u{1B}[?62;22c",
])
func terminalReportsAreNeverKeyPresses(report: String) {
    let bytes = Array(report.utf8)
    for split in 0..<bytes.count {
        var parser = TerminalInputParser()
        let events =
            parser.feed(Array("j".utf8) + bytes[..<split]) + parser.feed(Array(bytes[split...]) + Array("k".utf8))
        #expect(events == [.key(KeyPress(.character("j"))), .key(KeyPress(.character("k")))], "split at \(split)")
    }
}

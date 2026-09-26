import SwiftTUI

/// Swift-TUI adaptation of arkbig's BRAILLE_SIX glyph sequence.
/// https://github.com/arkbig/throbber-widgets-tui/blob/main/src/symbols.rs
/// See LICENSES/throbber-widgets-tui.txt for the original notice.
struct DiscoverySpinner: View {
    var body: some View {
        Spinner()
            .spinnerStyle(
                GlyphSpinnerStyle(
                    activeFrames: ["⠷", "⠯", "⠟", "⠻", "⠽", "⠾"],
                    interval: .milliseconds(100))
            )
            .foregroundStyle(.info)
    }
}

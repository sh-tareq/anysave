// Gives files a custom Finder icon:  swift set-file-icon.swift <icon.icns> <file>...
// Used by build.sh so the files in dist/ show the AnySave icon on this Mac.
import AppKit

let args = CommandLine.arguments
guard args.count > 2, let icon = NSImage(contentsOfFile: args[1]) else { exit(1) }
for file in args.dropFirst(2) where FileManager.default.fileExists(atPath: file) {
  _ = NSWorkspace.shared.setIcon(icon, forFile: file)
}

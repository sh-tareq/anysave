// Renders the AnySave logo (glossy red tile + raised 3D white download arrow)
// for every place it's used:
//   extension/icons/icon{16,32,48,128}.png                 Chrome extension
//   android/.../mipmap-*/ic_launcher.png                    launchers without adaptive icons (e.g. some MIUI)
//   android/.../mipmap-*/ic_launcher_{background,foreground}.png   adaptive icon layers
//   android/.../drawable-nodpi/vidgrab_logo.png             in-app logo
//   dist-assets/vidgrab-logo-1024.png                       for docs / sharing
// Run from the repo root:  swift scripts/make-icons.swift
import AppKit

let root = FileManager.default.currentDirectoryPath
let res = "\(root)/android/app/src/main/res"

// ---------- palette ----------
let redTop = NSColor(red: 1.00, green: 0.42, blue: 0.40, alpha: 1)
let redBottom = NSColor(red: 0.80, green: 0.12, blue: 0.20, alpha: 1)
let extrude = NSColor(red: 0.55, green: 0.05, blue: 0.12, alpha: 1)

// ---------- glyph (arrow + tray) in a unit box, origin bottom-left ----------
func glyphPath(_ x: CGFloat, _ y: CGFloat, _ s: CGFloat) -> NSBezierPath {
    func p(_ u: CGFloat, _ v: CGFloat) -> CGPoint { CGPoint(x: x + u * s, y: y + v * s) }
    let path = NSBezierPath()
    // Arrow: shaft + head as one outline
    path.move(to: p(0.40, 0.92))
    path.line(to: p(0.60, 0.92))
    path.line(to: p(0.60, 0.52))
    path.line(to: p(0.80, 0.52))
    path.line(to: p(0.50, 0.20))
    path.line(to: p(0.20, 0.52))
    path.line(to: p(0.40, 0.52))
    path.close()
    // Tray
    let trayH = 0.10 * s
    path.append(NSBezierPath(roundedRect: CGRect(x: x + 0.14 * s, y: y + 0.0 * s, width: 0.72 * s, height: trayH),
                             xRadius: trayH / 2, yRadius: trayH / 2))
    return path
}

/// White glyph with a solid extruded side (3D depth), soft shadow and a top highlight.
func drawGlyph3D(_ x: CGFloat, _ y: CGFloat, _ s: CGFloat, px: CGFloat) {
    let ctx = NSGraphicsContext.current!.cgContext
    let depth = max(1, s * 0.055)
    // Soft shadow under everything
    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: -depth * 1.2), blur: s * 0.10,
                  color: NSColor.black.withAlphaComponent(0.35).cgColor)
    extrude.setFill()
    glyphPath(x + depth * 0.35, y - depth, s).fill()
    ctx.restoreGState()
    // Extrusion: stack of copies stepping toward the front face
    if px >= 32 {
        let steps = Int(max(2, depth))
        for i in stride(from: steps, through: 1, by: -1) {
            let t = CGFloat(i) / CGFloat(steps)
            extrude.blended(withFraction: 1 - t, of: NSColor(red: 0.75, green: 0.10, blue: 0.16, alpha: 1))!.setFill()
            glyphPath(x + depth * 0.35 * t, y - depth * t, s).fill()
        }
    }
    // Front face: white with a faint vertical gradient
    let face = glyphPath(x, y, s)
    NSGradient(starting: NSColor(white: 1.0, alpha: 1), ending: NSColor(white: 0.90, alpha: 1))!.draw(in: face, angle: -90)
}

/// Glossy rounded tile (or full-bleed square for adaptive backgrounds).
func drawTile(_ rect: CGRect, radius: CGFloat, px: CGFloat, dropShadow: Bool) {
    let ctx = NSGraphicsContext.current!.cgContext
    let tile = NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius)
    if dropShadow {
        ctx.saveGState()
        ctx.setShadow(offset: CGSize(width: 0, height: -rect.height * 0.025), blur: rect.height * 0.05,
                      color: NSColor.black.withAlphaComponent(0.30).cgColor)
        redBottom.setFill(); tile.fill()
        ctx.restoreGState()
    }
    NSGradient(starting: redTop, ending: redBottom)!.draw(in: tile, angle: -90)
    if px >= 32 {
        // Soft light from the top-left
        ctx.saveGState()
        tile.addClip()
        let glow = NSGradient(colors: [NSColor.white.withAlphaComponent(0.35), NSColor.white.withAlphaComponent(0)])!
        glow.draw(fromCenter: CGPoint(x: rect.minX + rect.width * 0.25, y: rect.maxY - rect.height * 0.15), radius: 0,
                  toCenter: CGPoint(x: rect.minX + rect.width * 0.25, y: rect.maxY - rect.height * 0.15), radius: rect.width * 0.75,
                  options: [])
        // Glossy band across the top half
        let gloss = NSBezierPath(ovalIn: CGRect(x: rect.minX - rect.width * 0.3, y: rect.midY + rect.height * 0.08,
                                                width: rect.width * 1.6, height: rect.height * 0.9))
        NSGradient(starting: NSColor.white.withAlphaComponent(0.22), ending: NSColor.white.withAlphaComponent(0.02))!
            .draw(in: gloss, angle: -90)
        ctx.restoreGState()
        // Thin inner rim
        NSColor.white.withAlphaComponent(0.18).setStroke()
        let rim = NSBezierPath(roundedRect: rect.insetBy(dx: rect.width * 0.01, dy: rect.width * 0.01),
                               xRadius: radius * 0.97, yRadius: radius * 0.97)
        rim.lineWidth = max(1, rect.width * 0.012)
        rim.stroke()
    }
}

func render(_ px: Int, _ draw: (CGFloat) -> Void) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    NSGraphicsContext.current!.cgContext.setShouldAntialias(true)
    NSGraphicsContext.current!.imageInterpolation = .high
    draw(CGFloat(px))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

func write(_ data: Data, _ path: String) {
    try? FileManager.default.createDirectory(atPath: (path as NSString).deletingLastPathComponent,
                                             withIntermediateDirectories: true)
    try! data.write(to: URL(fileURLWithPath: path))
    print("wrote \(path.replacingOccurrences(of: root + "/", with: ""))")
}

/// Full icon: tile with drop shadow + glyph.
func fullIcon(_ s: CGFloat) {
    let inset = s * (s <= 16 ? 0.02 : 0.06)
    let tile = CGRect(x: inset, y: inset * 1.3, width: s - 2 * inset, height: s - 2 * inset)
    drawTile(tile, radius: tile.width * 0.24, px: s, dropShadow: s >= 32)
    let g = tile.width * 0.60
    drawGlyph3D(tile.midX - g / 2, tile.midY - g * 0.50, g, px: s)
}

// Chrome extension
for px in [16, 32, 48, 128] { write(render(px, fullIcon), "\(root)/extension/icons/icon\(px).png") }

// Android legacy launcher icons (48dp) and adaptive layers (108dp)
let densities: [(String, CGFloat)] = [("mdpi", 1), ("hdpi", 1.5), ("xhdpi", 2), ("xxhdpi", 3), ("xxxhdpi", 4)]
for (name, scale) in densities {
    write(render(Int(48 * scale), fullIcon), "\(res)/mipmap-\(name)/ic_launcher.png")
    write(render(Int(108 * scale)) { s in
        drawTile(CGRect(x: 0, y: 0, width: s, height: s), radius: 0, px: s, dropShadow: false)
    }, "\(res)/mipmap-\(name)/ic_launcher_background.png")
    write(render(Int(108 * scale)) { s in
        let g = s * 0.40 // stays inside the 66dp safe zone
        drawGlyph3D(s / 2 - g / 2, s / 2 - g * 0.50, g, px: s)
    }, "\(res)/mipmap-\(name)/ic_launcher_foreground.png")
}

// In-app logo + a large copy for sharing
write(render(512, fullIcon), "\(res)/drawable-nodpi/vidgrab_logo.png")
write(render(1024, fullIcon), "\(root)/dist-assets/vidgrab-logo-1024.png")

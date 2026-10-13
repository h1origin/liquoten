import AppKit

enum Droplet {
    static func outline(in rect: NSRect) -> NSBezierPath {
        let w = min(rect.width, rect.height / 1.3)
        let r = w * 0.5
        let center = NSPoint(x: rect.midX, y: rect.minY + r)
        let tip = NSPoint(x: rect.midX, y: rect.minY + w * 1.3)
        let d = tip.y - center.y
        let alpha = acos(r / d)
        let deg = alpha * 180 / .pi
        let path = NSBezierPath()
        path.move(to: tip)
        let ctrl = d * 0.32
        let right = NSPoint(x: center.x + r * sin(alpha), y: center.y + r * cos(alpha))
        let left = NSPoint(x: center.x - r * sin(alpha), y: center.y + r * cos(alpha))
        path.curve(to: right,
                   controlPoint1: NSPoint(x: tip.x + ctrl * 0.18, y: tip.y - ctrl * 0.55),
                   controlPoint2: NSPoint(x: right.x - r * 0.05 * cos(alpha), y: right.y + ctrl * 0.45))
        path.appendArc(withCenter: center, radius: r, startAngle: 90 - deg, endAngle: 90 + deg - 360, clockwise: true)
        path.curve(to: tip,
                   controlPoint1: NSPoint(x: left.x + r * 0.05 * cos(alpha), y: left.y + ctrl * 0.45),
                   controlPoint2: NSPoint(x: tip.x - ctrl * 0.18, y: tip.y - ctrl * 0.55))
        path.close()
        return path
    }

    static func water(in rect: NSRect, level: CGFloat, amplitude: CGFloat, phase: CGFloat = 0) -> NSBezierPath {
        let bounds = outline(in: rect).bounds
        let y = bounds.minY + bounds.height * min(max(level, 0), 1)
        let path = NSBezierPath()
        path.move(to: NSPoint(x: bounds.minX - 1, y: bounds.minY - 1))
        path.line(to: NSPoint(x: bounds.minX - 1, y: y))
        let steps = 24
        for i in 0...steps {
            let t = CGFloat(i) / CGFloat(steps)
            let x = bounds.minX - 1 + (bounds.width + 2) * t
            path.line(to: NSPoint(x: x, y: y + amplitude * sin((t * 2 + phase) * .pi * 2)))
        }
        path.line(to: NSPoint(x: bounds.maxX + 1, y: bounds.minY - 1))
        path.close()
        return path
    }

    static func menuBarImage(remaining: Double?, tint: NSColor?) -> NSImage {
        let size = NSSize(width: 14, height: 18)
        let image = NSImage(size: size, flipped: false) { rect in
            let body = rect.insetBy(dx: 1, dy: 0.8)
            let shape = outline(in: body)
            shape.lineWidth = 1.4
            if let remaining {
                let level = CGFloat(remaining / 100)
                NSGraphicsContext.saveGraphicsState()
                shape.addClip()
                (tint ?? .black).setFill()
                water(in: body, level: level, amplitude: level > 0.02 && level < 0.98 ? 0.45 : 0).fill()
                NSGraphicsContext.restoreGraphicsState()
                (tint == nil ? NSColor.black : NSColor.labelColor).setStroke()
            } else {
                NSColor.black.withAlphaComponent(0.8).setStroke()
                shape.setLineDash([1.6, 1.4], count: 2, phase: 0)
            }
            shape.stroke()
            return true
        }
        image.isTemplate = tint == nil
        image.accessibilityDescription = "Liquoten"
        return image
    }

    static func appIcon(size: CGFloat) -> NSImage {
        NSImage(size: NSSize(width: size, height: size), flipped: false) { rect in
            let s = rect.width / 1024
            let tile = NSRect(x: 100 * s, y: 100 * s, width: 824 * s, height: 824 * s)
            let tilePath = NSBezierPath(roundedRect: tile, xRadius: 185 * s, yRadius: 185 * s)

            NSGraphicsContext.saveGraphicsState()
            let shadow = NSShadow()
            shadow.shadowColor = NSColor.black.withAlphaComponent(0.35)
            shadow.shadowOffset = NSSize(width: 0, height: -10 * s)
            shadow.shadowBlurRadius = 24 * s
            shadow.set()
            NSColor(srgbRed: 0.07, green: 0.11, blue: 0.20, alpha: 1).setFill()
            tilePath.fill()
            NSGraphicsContext.restoreGraphicsState()

            NSGradient(colors: [
                NSColor(srgbRed: 0.13, green: 0.20, blue: 0.35, alpha: 1),
                NSColor(srgbRed: 0.05, green: 0.08, blue: 0.16, alpha: 1),
            ])?.draw(in: tilePath, angle: -90)

            let body = NSRect(x: 292 * s, y: 190 * s, width: 440 * s, height: 600 * s)
            let shape = outline(in: body)

            NSGraphicsContext.saveGraphicsState()
            shape.addClip()
            NSColor.white.withAlphaComponent(0.06).setFill()
            shape.fill()
            NSColor(srgbRed: 0.35, green: 0.70, blue: 1.0, alpha: 0.35).setFill()
            water(in: body, level: 0.66, amplitude: 14 * s, phase: 0.35).fill()
            NSGradient(colors: [
                NSColor(srgbRed: 0.36, green: 0.80, blue: 1.0, alpha: 1),
                NSColor(srgbRed: 0.10, green: 0.42, blue: 0.90, alpha: 1),
            ])?.draw(in: water(in: body, level: 0.62, amplitude: 16 * s), angle: -90)
            NSGraphicsContext.restoreGraphicsState()

            shape.lineWidth = 26 * s
            NSColor.white.withAlphaComponent(0.92).setStroke()
            shape.stroke()

            let shine = NSBezierPath()
            let b = shape.bounds
            shine.move(to: NSPoint(x: b.minX + b.width * 0.24, y: b.minY + b.height * 0.34))
            shine.curve(to: NSPoint(x: b.minX + b.width * 0.40, y: b.minY + b.height * 0.14),
                        controlPoint1: NSPoint(x: b.minX + b.width * 0.22, y: b.minY + b.height * 0.22),
                        controlPoint2: NSPoint(x: b.minX + b.width * 0.30, y: b.minY + b.height * 0.15))
            shine.lineWidth = 22 * s
            shine.lineCapStyle = .round
            NSColor.white.withAlphaComponent(0.55).setStroke()
            shine.stroke()
            return true
        }
    }
}

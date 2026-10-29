import AppKit

enum Gourd {
    private static let bodyBottomRadius: CGFloat = 34
    private static let bodyTopRadius: CGFloat = 17
    private static let bodyBottomY: CGFloat = 20
    private static let bodyTopY: CGFloat = -24

    private static func point(_ x: CGFloat, _ y: CGFloat, origin: NSPoint, scale: CGFloat) -> NSPoint {
        NSPoint(x: origin.x + x * scale, y: origin.y - y * scale)
    }

    private static func bodyPath(origin: NSPoint, scale: CGFloat) -> NSBezierPath {
        let bottom = NSPoint(x: origin.x, y: origin.y - bodyBottomY * scale)
        let top = NSPoint(x: origin.x, y: origin.y - bodyTopY * scale)
        let rb = bodyBottomRadius * scale
        let rt = bodyTopRadius * scale
        let d = bodyBottomY - bodyTopY
        let a = (d * d + bodyBottomRadius * bodyBottomRadius - bodyTopRadius * bodyTopRadius) / (2 * d)
        let h = sqrt(bodyBottomRadius * bodyBottomRadius - a * a)
        let bottomAngle = atan2(a, h) * 180 / .pi
        let topAngle = atan2(-(d - a), h) * 180 / .pi

        let path = NSBezierPath()
        path.move(to: NSPoint(x: origin.x, y: top.y + rt))
        path.appendArc(withCenter: top, radius: rt, startAngle: 90, endAngle: topAngle, clockwise: true)
        path.appendArc(withCenter: bottom, radius: rb, startAngle: bottomAngle, endAngle: -180 - bottomAngle, clockwise: true)
        path.appendArc(withCenter: top, radius: rt, startAngle: 180 - topAngle, endAngle: 90, clockwise: true)
        path.close()
        return path
    }

    private static func innerBodyPath(origin: NSPoint, scale: CGFloat) -> NSBezierPath {
        let bottomRect = NSRect(
            x: origin.x - 30.5 * scale,
            y: origin.y - 20 * scale - 30.5 * scale,
            width: 61 * scale,
            height: 61 * scale
        )
        let topRect = NSRect(
            x: origin.x - 13.5 * scale,
            y: origin.y + 24 * scale - 13.5 * scale,
            width: 27 * scale,
            height: 27 * scale
        )
        let path = NSBezierPath(ovalIn: bottomRect)
        path.append(NSBezierPath(ovalIn: topRect))
        return path
    }

    private static func wavePoints(
        origin: NSPoint,
        scale: CGFloat,
        minX: CGFloat,
        maxX: CGFloat,
        y: CGFloat,
        amplitude: CGFloat
    ) -> [NSPoint] {
        let steps = 48
        return (0...steps).map { i in
            let t = CGFloat(i) / CGFloat(steps)
            let x = minX + (maxX - minX) * t
            let waveY = y - amplitude * sin(t * .pi * 2)
            return point(x, waveY, origin: origin, scale: scale)
        }
    }

    private static func waterPaths(
        origin: NSPoint,
        scale: CGFloat,
        minX: CGFloat,
        maxX: CGFloat,
        y: CGFloat,
        amplitude: CGFloat,
        bottomY: CGFloat
    ) -> (fill: NSBezierPath, edge: NSBezierPath) {
        let points = wavePoints(origin: origin, scale: scale, minX: minX, maxX: maxX, y: y, amplitude: amplitude)
        let fill = NSBezierPath()
        fill.move(to: point(minX, bottomY, origin: origin, scale: scale))
        for p in points {
            fill.line(to: p)
        }
        fill.line(to: point(maxX, bottomY, origin: origin, scale: scale))
        fill.close()

        let edge = NSBezierPath()
        edge.move(to: points[0])
        for p in points.dropFirst() {
            edge.line(to: p)
        }
        return (fill, edge)
    }

    private static func stemPath(origin: NSPoint, scale: CGFloat) -> NSBezierPath {
        NSBezierPath(
            roundedRect: NSRect(
                x: origin.x - 3 * scale,
                y: origin.y + 38 * scale,
                width: 6 * scale,
                height: 9 * scale
            ),
            xRadius: 2 * scale,
            yRadius: 2 * scale
        )
    }

    private static func leafPath(origin: NSPoint, scale: CGFloat) -> NSBezierPath {
        let p = { (x: CGFloat, y: CGFloat) in point(x, y, origin: origin, scale: scale) }
        let path = NSBezierPath()
        path.move(to: p(3, -42))
        path.curve(to: p(28, -45), controlPoint1: p(13, -54), controlPoint2: p(25, -51))
        path.curve(to: p(3, -42), controlPoint1: p(20, -38), controlPoint2: p(10, -38))
        path.close()
        return path
    }

    private static func highlightPath(origin: NSPoint, scale: CGFloat) -> NSBezierPath {
        let p = { (x: CGFloat, y: CGFloat) in point(x, y, origin: origin, scale: scale) }
        let start = p(-19, 32)
        let end = p(-8, 45)
        let radius = 21 * scale
        let dx = end.x - start.x
        let dy = end.y - start.y
        let d = hypot(dx, dy)
        let mid = NSPoint(x: (start.x + end.x) / 2, y: (start.y + end.y) / 2)
        let h = sqrt(max(0, radius * radius - d * d / 4))
        let center = NSPoint(x: mid.x - dy / d * h, y: mid.y + dx / d * h)
        let startAngle = atan2(start.y - center.y, start.x - center.x) * 180 / .pi
        let endAngle = atan2(end.y - center.y, end.x - center.x) * 180 / .pi
        let path = NSBezierPath()
        path.appendArc(withCenter: center, radius: radius, startAngle: startAngle, endAngle: endAngle, clockwise: false)
        return path
    }

    static func menuBarImage(remaining: Double?, tint: NSColor?, badge: Bool = false) -> NSImage {
        let baseWidth: CGFloat = 14
        let width: CGFloat = badge ? 18 : baseWidth
        let image = NSImage(size: NSSize(width: width, height: 18), flipped: false) { rect in
            let scale = rect.height / 116
            let origin = NSPoint(x: rect.minX + baseWidth / 2, y: rect.maxY - 60 * scale)
            let body = bodyPath(origin: origin, scale: scale)
            let ink = tint == nil ? NSColor.black : NSColor.labelColor

            if let remaining, remaining > 0 {
                let level = CGFloat(min(max(remaining / 100, 0), 1))
                let bodyBounds = body.bounds
                let innerBottom = bodyBounds.minY + 0.7
                let innerTop = bodyBounds.maxY - 0.7
                let innerHeight = max(0, innerTop - innerBottom)
                let amplitude = level > 0.05 && level < 0.95 ? 0.4 / scale : 0
                let waterHeight = min(innerHeight, max(innerHeight * level, 1 + amplitude))
                let waterTop = min(innerTop, innerBottom + waterHeight)
                let waterTopY = (origin.y - waterTop) / scale
                let paths = waterPaths(
                    origin: origin,
                    scale: scale,
                    minX: -34,
                    maxX: 34,
                    y: waterTopY,
                    amplitude: amplitude,
                    bottomY: 60
                )
                NSGraphicsContext.saveGraphicsState()
                body.addClip()
                (tint ?? .black).setFill()
                paths.fill.fill()
                NSGraphicsContext.restoreGraphicsState()
            }

            body.lineWidth = 1.4
            if remaining == nil {
                body.setLineDash([1.6, 1.4], count: 2, phase: 0)
                ink.withAlphaComponent(0.8).setStroke()
            } else {
                ink.setStroke()
            }
            body.stroke()

            ink.setFill()
            stemPath(origin: origin, scale: scale).fill()
            leafPath(origin: origin, scale: scale).fill()

            if badge {
                (tint == nil ? NSColor.black : NSColor.systemBlue).setFill()
                NSBezierPath(ovalIn: NSRect(x: rect.maxX - 5, y: rect.maxY - 6, width: 5, height: 5)).fill()
            }
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

            let scale = tile.height * 0.75 / 113
            let origin = NSPoint(x: tile.midX, y: tile.midY)
            let body = bodyPath(origin: origin, scale: scale)
            NSColor(srgbRed: 36 / 255, green: 55 / 255, blue: 95 / 255, alpha: 1).setFill()
            body.fill()

            let water = waterPaths(
                origin: origin,
                scale: scale,
                minX: -40,
                maxX: 40,
                y: 10,
                amplitude: 2.5,
                bottomY: 60
            )
            NSGraphicsContext.saveGraphicsState()
            innerBodyPath(origin: origin, scale: scale).addClip()
            NSColor(srgbRed: 59 / 255, green: 139 / 255, blue: 235 / 255, alpha: 1).setFill()
            water.fill.fill()
            water.edge.lineWidth = 3 * scale
            water.edge.lineJoinStyle = .round
            NSColor(srgbRed: 127 / 255, green: 184 / 255, blue: 245 / 255, alpha: 1).setStroke()
            water.edge.stroke()
            NSGraphicsContext.restoreGraphicsState()

            body.lineWidth = 5 * scale
            NSColor.white.withAlphaComponent(0.92).setStroke()
            body.stroke()

            let highlight = highlightPath(origin: origin, scale: scale)
            highlight.lineWidth = 3 * scale
            highlight.lineCapStyle = .round
            NSColor.white.withAlphaComponent(0.6).setStroke()
            highlight.stroke()

            NSColor.white.withAlphaComponent(0.92).setFill()
            stemPath(origin: origin, scale: scale).fill()
            NSColor(srgbRed: 76 / 255, green: 175 / 255, blue: 122 / 255, alpha: 1).setFill()
            leafPath(origin: origin, scale: scale).fill()
            return true
        }
    }
}
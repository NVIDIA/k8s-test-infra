# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

"""Cairo artwork for the Mokka GPU workflows video."""

import math

import cairo


WIDTH, HEIGHT = 1280, 720
PAPER = 0xFCF3E6
COFFEE = 0x211915
ESPRESSO = 0x18130F
CREAM = 0xF8EEE1
LATTE = 0xD5BCA6
BLUE = 0x80ACFB
DARK_BLUE = 0x254D99
GREEN = 0xC2E7A2
MONO = "monospace"
SANS = "sans-serif"


def color(ctx, value, alpha=1):
    ctx.set_source_rgba((value >> 16) / 255, ((value >> 8) & 255) / 255, (value & 255) / 255, alpha)


def rect(ctx, x, y, width, height, value, radius=0, alpha=1):
    ctx.new_sub_path()
    if radius:
        ctx.arc(x + width - radius, y + radius, radius, -math.pi / 2, 0)
        ctx.arc(x + width - radius, y + height - radius, radius, 0, math.pi / 2)
        ctx.arc(x + radius, y + height - radius, radius, math.pi / 2, math.pi)
        ctx.arc(x + radius, y + radius, radius, math.pi, math.pi * 1.5)
        ctx.close_path()
    else:
        ctx.rectangle(x, y, width, height)
    color(ctx, value, alpha)
    ctx.fill()


def line(ctx, x1, y1, x2, y2, value, width=1, alpha=1):
    ctx.move_to(x1, y1)
    ctx.line_to(x2, y2)
    ctx.set_line_width(width)
    color(ctx, value, alpha)
    ctx.stroke()


def text(ctx, x, y, words, size, value, *, bold=False, font=SANS, center=False, limit=None, alpha=1):
    weight = cairo.FONT_WEIGHT_BOLD if bold else cairo.FONT_WEIGHT_NORMAL
    ctx.select_font_face(font, cairo.FONT_SLANT_NORMAL, weight)
    ctx.set_font_size(size)
    ext = ctx.text_extents(words)
    if limit and ext.width > limit:
        ctx.set_font_size(size * limit / ext.width)
        ext = ctx.text_extents(words)
    if center:
        x -= ext.width / 2 + ext.x_bearing
    ctx.move_to(x, y)
    color(ctx, value, alpha)
    ctx.show_text(words)


def circle(ctx, x, y, radius, value, alpha):
    ctx.new_sub_path()
    ctx.arc(x, y, radius, 0, math.tau)
    color(ctx, value, alpha)
    ctx.fill()


def brand_mark(ctx, logo, x, y, width):
    ctx.save()
    ctx.translate(x, y)
    scale = width / logo.get_width()
    ctx.scale(scale, scale)
    ctx.set_source_surface(logo)
    ctx.get_source().set_filter(cairo.FILTER_BILINEAR)
    ctx.paint()
    ctx.restore()


def presenter(ctx, sprite, x, y, width, t, mouth):
    """Composite the robot with a blinking visor and a narration-driven mouth."""
    ctx.save()
    bob = width * (0.012 * math.sin(t * 3.2) - 0.009 * mouth)
    ctx.translate(x, y + bob)
    scale = width / sprite.get_width()
    ctx.scale(scale, scale)
    ctx.set_source_surface(sprite)
    ctx.get_source().set_filter(cairo.FILTER_BILINEAR)
    ctx.paint()

    # These coordinates align with the eyes and smile in the presenter sprite.
    if (t + 0.65) % 4.4 < 0.13:
        for eye in (522, 732):
            circle(ctx, eye, 435, 50, 0x161D17, 1)
            ctx.move_to(eye - 27, 438)
            ctx.curve_to(eye - 10, 454, eye + 10, 454, eye + 27, 438)
            ctx.set_line_cap(cairo.LINE_CAP_ROUND)
            ctx.set_line_width(8)
            color(ctx, 0xC6EE22)
            ctx.stroke()

    if mouth > 0.04:
        alpha = ease((mouth - 0.04) / 0.22)
        rect(ctx, 591, 493, 72, 34, 0x15191B, 11, alpha)
        ctx.translate(627, 510)
        ctx.scale(34 - 8 * mouth, 8 + 17 * mouth)
        circle(ctx, 0, 0, 1, 0xC6EE22, alpha)
        circle(ctx, 0, 0, 0.75, 0x141A16, alpha)
        circle(ctx, 0, 0.38, 0.25, 0xA2C53A, alpha * mouth)
    ctx.restore()


def ease(t):
    t = max(0, min(1, t))
    return t * t * (3 - 2 * t)


def opening(ctx, logo, copy):
    rect(ctx, 0, 0, WIDTH, HEIGHT, PAPER)
    circle(ctx, 1010, 329, 211, 0xE5D2BE, 0.85)
    circle(ctx, 1010, 329, 178, 0xF3E4D4, 1)
    brand_mark(ctx, logo, 872, 174, 275)
    text(ctx, 73, 102, "MOKKA", 23, DARK_BLUE, bold=True)
    for index, row in enumerate(copy["headline"]):
        text(ctx, 70, 191 + index * 72, row, 65, COFFEE, bold=True, limit=725)
    line(ctx, 76, 391, 145, 391, DARK_BLUE, 3)
    x = 76
    for index, part in enumerate(copy["library"]):
        text(ctx, x, 445, part, 30, DARK_BLUE, bold=index == 1)
        x += ctx.text_extents(part).x_advance
    for index, row in enumerate(copy["details"]):
        text(ctx, 76, 486 + index * 41, row, 30, DARK_BLUE, limit=735)


def terminal(ctx, y, height, label):
    rect(ctx, 74, y, 1132, height, 0x352A24, 22)
    rect(ctx, 75, y + 1, 1130, height - 2, ESPRESSO, 21)
    rect(ctx, 75, y + 1, 1130, 55, 0x352A24, 20)
    rect(ctx, 75, y + 29, 1130, 26, 0x352A24)
    circle(ctx, 103, y + 28, 5, 0xDC9976, 1)
    circle(ctx, 120, y + 28, 5, 0xE9BA79, 1)
    circle(ctx, 137, y + 28, 5, 0xAAC879, 1)
    text(ctx, 160, y + 34, label, 17, LATTE, font=MONO)


def install(ctx, spec, t):
    rect(ctx, 0, 0, WIDTH, HEIGHT, COFFEE)
    text(ctx, 72, 115, "Install Mokka with Helm", 56, CREAM, bold=True)
    terminal(ctx, 169, 400, "CPU-only Kind cluster")
    commands = spec["install"]
    for index, command in enumerate(commands):
        y = 284 + index * 57
        continued = index > 0 and commands[index - 1].endswith("\\")
        prompt = "  " if continued else "$ "
        reveal = (0, 0.45, 0.65, 0.85, 1.4)[index]
        alpha = ease((t - reveal) / 0.4)
        text(ctx, 106, y, prompt + command, 25, CREAM, font=MONO, limit=1061, alpha=alpha)


def result(ctx, spec, output, t):
    rect(ctx, 0, 0, WIDTH, HEIGHT, COFFEE)
    text(ctx, 73, 105, "See what nvidia-smi reports", 54, CREAM, bold=True)
    terminal(ctx, 151, 446, "Captured from kind-mokka-video")
    text(ctx, 106, 261, "$ " + spec["query"], 24, BLUE, font=MONO, limit=1061)
    line(ctx, 106, 290, 1174, 290, 0x44352D)
    size = 24
    ctx.select_font_face(MONO)
    ctx.set_font_size(size)
    width = max(ctx.text_extents(row).width for row in output)
    size = min(size, size * 1050 / width)
    for index, row in enumerate(output):
        alpha = ease((t - 2.2 - index * 0.37) / 0.32)
        y = 341 + index * 63
        rect(ctx, 93, y - 34, 1094, 51, 0x283124, 9, 0.9 * alpha)
        text(ctx, 106, y, row, size, GREEN, font=MONO, alpha=alpha)


def operator(ctx, capture, t):
    rect(ctx, 0, 0, WIDTH, HEIGHT, PAPER)
    text(ctx, 73, 119, "NVIDIA GPU Operator", 57, COFFEE, bold=True)
    text(ctx, 76, 174, "Mokka supplies the driver and runtime", 28, DARK_BLUE)
    results = (
        ("Device plugin", f'{capture["allocatable"]} allocatable GPUs', "nvidia.com/gpu"),
        ("GPU Feature Discovery", capture["product"], "nvidia.com/gpu.product"),
        ("DCGM Exporter", f'GPU 0 · {capture["gpu_temperature_c"]} °C', capture["temperature_metric"]),
    )
    for index, (label, value, detail) in enumerate(results):
        x = 75 + index * 389
        rect(ctx, x, 227, 351, 251, 0xE0CEBB, 20)
        rect(ctx, x + 1, 228, 349, 249, 0xFFF9F1, 19)
        text(ctx, x + 23, 278, label, 25, DARK_BLUE, bold=True, limit=306)
        alpha = ease((t - (1.6, 3.6, 5.7)[index]) / 0.55)
        text(ctx, x + 23, 367, value, 38, COFFEE, bold=True, limit=306, alpha=alpha)
        text(ctx, x + 23, 419, detail, 18, DARK_BLUE, limit=306, alpha=alpha)
    text(ctx, 76, 534, "GPU Operator validator ready", 25, DARK_BLUE,
         alpha=ease((t - 6.8) / 0.55))


def dra(ctx, capture, t):
    rect(ctx, 0, 0, WIDTH, HEIGHT, COFFEE)
    text(ctx, 73, 104, "Dynamic Resource Allocation", 54, CREAM, bold=True)
    text(ctx, 76, 152, "NVIDIA DRA driver · Separate Kind cluster", 25, BLUE)
    steps = (
        ("ResourceSlices", f'{capture["published_devices"]} GPUs published'),
        ("ResourceClaim", capture["device_class"]),
        (capture["pod"], capture["pod_phase"]),
    )
    for index, (label, value) in enumerate(steps):
        x = 74 + index * 390
        alpha = ease((t - (2.2, 3.7, 5.1)[index]) / 0.55)
        rect(ctx, x, 203, 352, 122, 0x382F29, 15, alpha)
        text(ctx, x + 20, 249, label, 22, BLUE, bold=True, alpha=alpha)
        text(ctx, x + 20, 293, value, 28, CREAM, bold=True, limit=310, alpha=alpha)
        if index:
            line(ctx, x - 30, 264, x - 8, 264, BLUE, 2, alpha)
            line(ctx, x - 14, 258, x - 8, 264, BLUE, 2, alpha)
            line(ctx, x - 14, 270, x - 8, 264, BLUE, 2, alpha)
    if t >= 5.6:
        terminal(ctx, 349, 213, "Inside the pod")
        text(ctx, 106, 450, "$ kubectl exec " + capture["pod"] + " -- nvidia-smi -L", 23,
             BLUE, font=MONO, limit=1061)
    alpha = ease((t - 6.7) / 0.45)
    if alpha:
        text(ctx, 106, 510, capture["nvidia_smi"], 23, GREEN, font=MONO, limit=1061, alpha=alpha)
        text(ctx, 76, 598, "Pod UUID matches the ResourceClaim allocation", 24, GREEN,
             alpha=ease((t - 7.8) / 0.45))


def ci(ctx, guide, t):
    rect(ctx, 0, 0, WIDTH, HEIGHT, PAPER)
    text(ctx, 73, 104, "Run GPU tests in CI/CD", 55, COFFEE, bold=True)
    text(ctx, 76, 156, "GitHub Actions", 25, DARK_BLUE, bold=True)
    text(ctx, 76, 194, "runs-on: ubuntu-latest", 23, DARK_BLUE, font=MONO)
    steps = (
        ("Create a Kind cluster", "helm/kind-action@v1", 0.3),
        ("Install a pinned Mokka chart", "--version 0.7.0 --set gpu.profile=gb300", 1.8),
        ("Wait for the DaemonSet", "--wait --timeout 5m", 3.4),
        ("Run your tests", "./run-my-gpu-tests.sh", 5.0),
    )
    for index, (label, detail, reveal) in enumerate(steps):
        x = 74 + 576 * (index % 2)
        y = 238 + 160 * (index // 2)
        alpha = ease((t - reveal) / 0.45)
        rect(ctx, x, y, 554, 141, 0xE0CEBB, 17, alpha)
        rect(ctx, x + 1, y + 1, 552, 139, 0xFFF9F1, 16, alpha)
        text(ctx, x + 22, y + 51, label, 29, COFFEE, bold=True, alpha=alpha)
        text(ctx, x + 22, y + 95, detail, 21, DARK_BLUE, font=MONO, limit=510, alpha=alpha)
    text(ctx, 76, 588, guide, 21, DARK_BLUE, font=MONO)


def features(ctx, copy, t):
    rect(ctx, 0, 0, WIDTH, HEIGHT, COFFEE)
    text(ctx, 73, 106, copy["heading"], 55, CREAM, bold=True)
    for index, topic in enumerate(copy["topics"]):
        x = 74 + 576 * (index % 2)
        y = 170 + 195 * (index // 2)
        alpha = ease((t - index * 0.32) / 0.4)
        rect(ctx, x, y, 554, 175, 0x382F29, 17, alpha)
        text(ctx, x + 23, y + 51, topic["title"], 30, BLUE, bold=True, alpha=alpha)
        for row, words in enumerate(topic["lines"]):
            text(ctx, x + 23, y + 96 + row * 34, words, 24, CREAM, limit=508, alpha=alpha)


def ending(ctx, logo, spec):
    rect(ctx, 0, 0, WIDTH, HEIGHT, COFFEE)
    circle(ctx, 1002, 328, 181, 0x4B382E, 0.8)
    brand_mark(ctx, logo, 880, 187, 243)
    text(ctx, 72, 199, "Explore Mokka", 75, CREAM, bold=True)
    for index, (label, url) in enumerate((
        ("GitHub repository", spec["repository"]),
        ("Documentation and guides", spec["guides"]),
    )):
        y = 263 + index * 141
        text(ctx, 76, y, label, 26, CREAM, limit=751)
        rect(ctx, 75, y + 20, 734, 62, CREAM, 14)
        text(ctx, 100, y + 59, url, 24, DARK_BLUE, font=MONO, bold=True, limit=683)


def draw(ctx, spec, logo, mascot, output, integrations, index, local, t, mouth, caption):
    scenes = (
        lambda: opening(ctx, logo, spec["opening"]),
        lambda: install(ctx, spec, local),
        lambda: result(ctx, spec, output, local),
        lambda: operator(ctx, integrations["operator"], local),
        lambda: dra(ctx, integrations["dra"], local),
        lambda: ci(ctx, spec["ci_guide"], local),
        lambda: features(ctx, spec["features"], local),
        lambda: ending(ctx, logo, spec),
    )
    scenes[index]()
    if 0 < index < len(scenes) - 1:
        if local < 0.34:
            width = WIDTH * (1 - ease(local / 0.34))
            rect(ctx, WIDTH - width, 0, width, HEIGHT, DARK_BLUE)
        presenter(ctx, mascot, 1068, 2, 148, t, mouth)
    if caption:
        rect(ctx, 74, 625, 1132, 61, ESPRESSO, 12, 0.96)
        text(ctx, WIDTH / 2, 665, caption, 27, CREAM, bold=True, center=True, limit=1084)

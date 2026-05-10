#!/usr/bin/env python3
from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


W, H = 1080, 1920
BG = "#0B1020"
PANEL = "#101827"
WHITE = "#F8FAFC"
MUTED = "#CBD5E1"
BLUE = "#7DD3FC"


def font(size: int) -> ImageFont.FreeTypeFont:
    for path in (
        "/System/Library/Fonts/Hiragino Sans GB.ttc",
        "/System/Library/Fonts/STHeiti Medium.ttc",
        "/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
    ):
        if Path(path).exists():
            return ImageFont.truetype(path, size=size)
    return ImageFont.load_default(size=size)


def text_size(draw: ImageDraw.ImageDraw, text: str, fnt: ImageFont.ImageFont) -> tuple[int, int]:
    box = draw.textbbox((0, 0), text, font=fnt)
    return box[2] - box[0], box[3] - box[1]


def center(draw: ImageDraw.ImageDraw, y: int, text: str, size: int, fill: str = WHITE) -> None:
    fnt = font(size)
    tw, th = text_size(draw, text, fnt)
    draw.text(((W - tw) / 2, y), text, font=fnt, fill=fill)


def line(draw: ImageDraw.ImageDraw, x: int, y: int, text: str, size: int, fill: str = WHITE) -> None:
    draw.text((x, y), text, font=font(size), fill=fill)


def base(github: str) -> tuple[Image.Image, ImageDraw.ImageDraw]:
    img = Image.new("RGB", (W, H), BG)
    draw = ImageDraw.Draw(img)
    draw.rounded_rectangle((58, 120, 1022, 1800), radius=24, fill=PANEL, outline="#38BDF8", width=3)
    for i, color in enumerate(("#FB7185", "#FBBF24", "#34D399")):
        draw.ellipse((88 + i * 34, 157, 106 + i * 34, 175), fill=color)
    line(draw, 88, 214, "Easy Terminal", 54)
    line(draw, 88, 284, github, 28, "#93C5FD")
    return img, draw


def terminal(draw: ImageDraw.ImageDraw, xy: tuple[int, int, int, int], rows: list[tuple[str, str]]) -> None:
    draw.rounded_rectangle(xy, radius=14, fill="#020617")
    x, y = xy[0] + 42, xy[1] + 48
    for text, color in rows:
        line(draw, x, y, text, 38, color)
        y += 68


def card(draw: ImageDraw.ImageDraw, xy: tuple[int, int, int, int], title: str, items: list[tuple[str, str]]) -> None:
    draw.rounded_rectangle(xy, radius=18, fill="#020617")
    center_x = (xy[0] + xy[2]) // 2
    f = font(38)
    tw, _ = text_size(draw, title, f)
    draw.text((center_x - tw / 2, xy[1] + 42), title, font=f, fill=WHITE)
    y = xy[1] + 120
    for item, color in items:
        line(draw, xy[0] + 48, y, item, 34, color)
        y += 74


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out-dir", required=True)
    parser.add_argument("--github", default="github.com/elevenlj/easy_terminal")
    args = parser.parse_args()
    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)

    slides: list[tuple[str, float]] = []

    img, draw = base(args.github)
    center(draw, 555, "比 OpenClaw、Hermes", 62)
    center(draw, 635, "更接地气的 Agent", 66, BLUE)
    center(draw, 780, "不是再做一个新的智能体", 44, MUTED)
    center(draw, 855, "而是先控制终端", 58, BLUE)
    img.save(out / "slide_01.png")
    slides.append(("slide_01.png", 6.0))

    img, draw = base(args.github)
    center(draw, 500, "真正工作的 Agent", 56)
    center(draw, 580, "本来就跑在终端里", 56, BLUE)
    terminal(draw, (166, 740, 914, 1032), [
        ("$ opencode", "#34D399"),
        ("$ codex", "#7DD3FC"),
        ("$ claude-code", "#FBBF24"),
        ("$ gemini", "#FB7185"),
    ])
    center(draw, 1155, "终端能跑什么，Easy Terminal 就能远程使用什么", 36, MUTED)
    img.save(out / "slide_02.png")
    slides.append(("slide_02.png", 8.0))

    img, draw = base(args.github)
    center(draw, 430, "和云端 Agent 的区别", 58)
    card(draw, (120, 610, 500, 1120), "云端 Agent", [("依赖 API key", "#FCA5A5"), ("单一平台能力", "#FCA5A5"), ("任务结束后通知", "#FCA5A5")])
    card(draw, (580, 610, 960, 1120), "Easy Terminal", [("本地或服务器执行", "#CCFBF1"), ("任意 CLI Agent", "#CCFBF1"), ("实时可见可接管", "#CCFBF1")])
    center(draw, 1240, "不是黑盒执行，而是掌控自己的工作环境", 38)
    img.save(out / "slide_03.png")
    slides.append(("slide_03.png", 10.0))

    img, draw = base(args.github)
    center(draw, 465, "成本更低", 64)
    center(draw, 570, "免费 / 开源 / 本地 CLI Agent 都能接入", 40, BLUE)
    center(draw, 650, "不被某一个云端平台绑定", 40, MUTED)
    terminal(draw, (170, 820, 910, 990), [
        ("OpenCode  /  Codex CLI  /  Claude Code", WHITE),
        ("本地模型  /  内部脚本  /  自动化工具", WHITE),
    ])
    img.save(out / "slide_04.png")
    slides.append(("slide_04.png", 9.0))

    img, draw = base(args.github)
    center(draw, 420, "能力更强", 64)
    center(draw, 525, "控制终端，就连接真实开发环境", 42, BLUE)
    card(draw, (150, 690, 930, 1110), "", [("启动服务        看日志", "#34D399"), ("跑测试          改代码", "#FBBF24"), ("执行脚本        操作 Git", "#A78BFA")])
    center(draw, 1210, "终端能做什么，Agent 就能做什么", 42)
    img.save(out / "slide_05.png")
    slides.append(("slide_05.png", 11.0))

    img, draw = base(args.github)
    center(draw, 420, "飞书里实时看，随时接管", 56)
    draw.rounded_rectangle((205, 590, 875, 1240), radius=34, fill="#0F172A")
    draw.rounded_rectangle((250, 680, 715, 762), radius=22, fill="#1E293B")
    line(draw, 280, 702, "Agent 正在跑测试...", 32, "#E2E8F0")
    draw.rounded_rectangle((365, 805, 800, 887), radius=22, fill="#0F766E")
    line(draw, 405, 827, "先别改数据库", 32)
    draw.rounded_rectangle((250, 930, 770, 1012), radius=22, fill="#1E293B")
    line(draw, 280, 952, "收到，切换为只读检查", 32, "#E2E8F0")
    center(draw, 1340, "不是等结束后通知，而是执行过程全程可见", 38, BLUE)
    img.save(out / "slide_06.png")
    slides.append(("slide_06.png", 11.0))

    img, draw = base(args.github)
    center(draw, 410, "多会话并行", 60)
    boxes = [("前端服务", "#34D399", 132, 610), ("后端日志", "#7DD3FC", 588, 610), ("Agent 修复", "#FBBF24", 132, 890), ("自动测试", "#FB7185", 588, 890)]
    for title, color, x, y in boxes:
        draw.rounded_rectangle((x, y, x + 360, y + 220), radius=18, fill="#020617")
        line(draw, x + 82, y + 85, title, 36, color)
    center(draw, 1250, "真实开发不是单线程，Easy Terminal 也不是", 38, MUTED)
    img.save(out / "slide_07.png")
    slides.append(("slide_07.png", 8.0))

    img, draw = base(args.github)
    center(draw, 530, "Easy Terminal", 72)
    center(draw, 660, "不是又一个 Agent", 46, MUTED)
    center(draw, 745, "它是所有终端 Agent 的入口", 52, BLUE)
    center(draw, 940, "控制终端 = 控制所有能在终端里运行的智能体", 40)
    center(draw, 1160, args.github, 34, "#93C5FD")
    img.save(out / "slide_08.png")
    slides.append(("slide_08.png", 12.2))

    concat = out / "slides.txt"
    with concat.open("w", encoding="utf-8") as f:
        for name, duration in slides:
            f.write(f"file '{(out / name).as_posix()}'\n")
            f.write(f"duration {duration}\n")
        f.write(f"file '{(out / slides[-1][0]).as_posix()}'\n")


if __name__ == "__main__":
    main()

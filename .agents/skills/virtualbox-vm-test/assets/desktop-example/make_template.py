"""Regenerate the synthetic, project-owned image target using Pillow."""

from pathlib import Path

from PIL import Image, ImageDraw


def main():
    image = Image.new("RGB", (96, 48), "#15324f")
    draw = ImageDraw.Draw(image)
    draw.rectangle((2, 2, 93, 45), outline="#f7f1dc", width=2)
    draw.rectangle((10, 10, 30, 36), fill="#f5b942")
    draw.ellipse((38, 9, 64, 35), fill="#4dd6b0")
    draw.polygon([(72, 34), (82, 10), (90, 34)], fill="#f77777")
    image.save(Path(__file__).with_name("target.png"))


if __name__ == "__main__":
    main()

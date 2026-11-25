# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

"""Render the Mokka GPU workflows video or prepare its local speech assets."""

import argparse
import json
import os
from pathlib import Path
import subprocess
from tempfile import TemporaryDirectory

import cairo
import numpy as np

import artwork
from sound.music import mix_audio
from sound.prepare_assets import prepare_assets
from sound.voice import RATE, narration, voice_engine


HERE = Path(__file__).resolve().parent
SPEC = HERE / "episodes/01-mokka-gpu-workflows.json"
MASCOT = HERE / "artwork/mokka-robot.png"
LOGO = HERE.parents[1] / "docs/img/logo.png"
WIDTH, HEIGHT, FPS, DURATION = 1920, 1080, 30, 60


def draw_frames(stream, spec, gpu_lines, integrations, edges, voice, captions):
    surface = cairo.ImageSurface(cairo.FORMAT_RGB24, WIDTH, HEIGHT)
    ctx = cairo.Context(surface)
    ctx.scale(WIDTH / artwork.WIDTH, HEIGHT / artwork.HEIGHT)
    mascot = cairo.ImageSurface.create_from_png(str(MASCOT))
    logo = cairo.ImageSurface.create_from_png(str(LOGO))
    # Use the dry narration so the robot rests between phrases even as the score plays.
    level = np.sqrt(np.mean(voice.reshape(DURATION * FPS, -1) ** 2, axis=1))
    mouth = np.clip((np.convolve(level, (0.18, 0.64, 0.18), mode="same") - 0.012) * 9, 0, 1)
    # Encode each frame directly to avoid a directory of uncompressed images.
    pixels = surface.get_data()
    for index, (start, stop) in enumerate(zip(edges, edges[1:])):
        for frame in range(int(start * FPS), int(stop * FPS)):
            t = frame / FPS
            caption = next((text for lo, hi, text in captions if lo <= t < hi), None)
            artwork.draw(ctx, spec, logo, mascot, gpu_lines, integrations, index, t - start, t, mouth[frame], caption)
            surface.flush()
            stream.write(pixels)


def verify(path):
    result = subprocess.run(
        ["ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", str(path)],
        check=True, capture_output=True, timeout=30,
    )
    probe = json.loads(result.stdout)
    video, audio = probe["streams"]
    actual = (
        video["codec_name"], video["width"], video["height"], video["avg_frame_rate"],
        int(video["nb_frames"]), audio["codec_name"], audio["channels"], float(probe["format"]["duration"]),
    )
    if actual != ("h264", WIDTH, HEIGHT, f"{FPS}/1", DURATION * FPS, "aac", 2, DURATION):
        raise ValueError(f"Unexpected encoded video properties: {actual!r}")
    subprocess.run(
        ["ffmpeg", "-v", "error", "-nostdin", "-xerror", "-i", str(path), "-f", "null", "-"],
        check=True, timeout=120,
    )


def render(cache, output):
    if output.exists():
        raise FileExistsError(f"{output} already exists. Choose a fresh output path.")
    spec = json.loads(SPEC.read_text(encoding="utf-8"))
    edges = [scene["start"] for scene in spec["scenes"]] + [DURATION]
    if edges[0] != 0 or len(edges) != 9 or any(a >= b for a, b in zip(edges, edges[1:])):
        raise ValueError("Expected eight ordered scenes covering 0 through 60 seconds")
    gpu_lines = (SPEC.parent / spec["capture"]).read_text(encoding="utf-8").splitlines()
    if len(gpu_lines) != 4 or any(not line.startswith(f"GPU {i}: NVIDIA GB300 NVL ")
                                  for i, line in enumerate(gpu_lines)):
        raise ValueError("Expected four ordered GB300 NVL lines from the quick-start capture")
    integrations = json.loads((SPEC.parent / spec["integrations"]).read_text(encoding="utf-8"))
    operator, dra = integrations["operator"], integrations["dra"]
    if operator["allocatable"] != 4 or not operator["validator_ready"]:
        raise ValueError("Expected a ready GPU Operator validator and four allocatable GPUs")
    if (dra["published_devices"] != 4 or dra["pod_phase"] != "Running"
            or not dra["nvidia_smi"].endswith(f'(UUID: {dra["allocated_uuid"]})')):
        raise ValueError("Expected four DRA devices and a running pod that sees its allocated GPU")
    print("Synthesizing narration", flush=True)
    voice, captions = narration(voice_engine(cache), spec, edges)
    output.parent.mkdir(parents=True, exist_ok=True)
    with TemporaryDirectory(dir=output.parent) as staging:
        audio = Path(staging) / "soundtrack.wav"
        staged = Path(staging) / "mokka.mp4"
        mix_audio(audio, voice, RATE)
        command = [
            "ffmpeg", "-nostdin", "-v", "error",
            "-f", "rawvideo", "-pixel_format", "bgr0", "-video_size", f"{WIDTH}x{HEIGHT}",
            "-framerate", str(FPS), "-i", "pipe:0", "-i", str(audio),
            "-c:v", "libx264", "-preset", "veryfast", "-crf", "17", "-pix_fmt", "yuv420p",
            "-vf", "scale=out_color_matrix=bt709", "-colorspace", "bt709",
            "-color_trc", "bt709", "-color_primaries", "bt709",
            "-c:a", "aac", "-ar", "48000", "-b:a", "160k",
            "-metadata", f"title={spec['title']} | Mokka", "-metadata", "language=eng",
            "-metadata:s:a:0", "language=eng", "-movflags", "+faststart", str(staged),
        ]
        print("Drawing and encoding 1,800 frames", flush=True)
        with subprocess.Popen(command, stdin=subprocess.PIPE) as encoder:
            try:
                draw_frames(encoder.stdin, spec, gpu_lines, integrations, edges, voice, captions)
                encoder.stdin.close()
                if encoder.wait(timeout=120):
                    raise RuntimeError("FFmpeg could not encode the video")
            except BaseException:
                encoder.kill()
                raise
        verify(staged)
        os.link(staged, output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--assets", required=True, type=Path, help="directory for the cached Kokoro model and voices")
    action = parser.add_mutually_exclusive_group(required=True)
    action.add_argument("--prepare-assets", action="store_true", help="download and verify the voice assets")
    action.add_argument("--output", type=Path, help="write the checked MP4 to this path")
    args = parser.parse_args()
    try:
        if args.prepare_assets:
            prepare_assets(args.assets)
        else:
            render(args.assets, args.output)
            print(f"Verified {args.output} (60 seconds, 1080p, 30 fps, stereo)")
    except (OSError, RuntimeError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"{error}\n")


if __name__ == "__main__":
    main()

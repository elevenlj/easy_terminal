#!/usr/bin/env python3
from __future__ import annotations

import argparse
from pathlib import Path

import numpy as np
import soundfile as sf
import torch
from qwen_tts import Qwen3TTSModel


def split_script(text: str) -> list[str]:
    chunks: list[str] = []
    for raw in text.splitlines():
        line = raw.strip()
        if line:
            chunks.append(line)
    return chunks


def main() -> None:
    parser = argparse.ArgumentParser(description="Generate native Qwen3 TTS voiceover.")
    parser.add_argument("--script", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument(
        "--model",
        default="Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice",
        help="Use a CustomVoice model for native speakers, not Base voice cloning.",
    )
    parser.add_argument("--speaker", default="Uncle_Fu")
    parser.add_argument("--language", default="Chinese")
    parser.add_argument(
        "--instruct",
        default="",
    )
    args = parser.parse_args()

    script = Path(args.script).read_text(encoding="utf-8")
    chunks = split_script(script)
    if not chunks:
        raise SystemExit("voiceover script is empty")

    model = Qwen3TTSModel.from_pretrained(
        args.model,
        device_map="mps",
        dtype=torch.float32,
    )

    speakers = model.get_supported_speakers() or []
    if speakers and args.speaker.lower() not in {speaker.lower() for speaker in speakers}:
        raise SystemExit(f"unsupported speaker {args.speaker!r}; supported: {speakers}")

    output_segments: list[np.ndarray] = []
    sample_rate = 24000
    silence = np.zeros(int(sample_rate * 0.28), dtype=np.float32)

    for chunk in chunks:
        kwargs = {
            "text": chunk,
            "language": args.language,
            "speaker": args.speaker,
        }
        if args.instruct:
            kwargs["instruct"] = args.instruct
        wavs, sample_rate = model.generate_custom_voice(**kwargs)
        output_segments.append(np.asarray(wavs[0], dtype=np.float32))
        output_segments.append(np.zeros(int(sample_rate * 0.28), dtype=np.float32))

    audio = np.concatenate(output_segments) if output_segments else silence
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    sf.write(output, audio, sample_rate)
    print(f"wrote {output} ({len(audio) / sample_rate:.2f}s, speaker={args.speaker})")


if __name__ == "__main__":
    main()

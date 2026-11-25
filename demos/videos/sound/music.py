# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

"""Generate and mix the Mokka backing score."""

import wave

import numpy as np


def mix_audio(path, voice, rate):
    """Add a quiet, original four-chord score and write stereo PCM."""
    audio = np.repeat(voice[:, None], 2, axis=1)
    chords = ((48, 55, 64, 67), (45, 52, 60, 64), (41, 53, 60, 67), (43, 55, 62, 69))
    for beat in range(96):
        start = beat * 5 * rate // 8
        chord = chords[(beat // 8) % len(chords)]
        note = chord[(0, 2, 1, 3, 0, 1, 2, 3)[beat % 8]]
        length = min(rate * 2, len(voice) - start)
        time = np.arange(length) / rate
        phase = 2 * np.pi * 440 * 2 ** ((note - 69) / 12) * time
        envelope = np.minimum(time / 0.024, 1) * np.exp(-2.6 * time)
        tone = (np.sin(phase) + 0.27 * np.sin(2 * phase)) * envelope
        tone *= 0.029 if beat >= 84 else 0.017
        pan = (1.0, 0.70) if beat % 2 == 0 else (0.70, 1.0)
        audio[start:start + length] += tone[:, None] * pan
    fade = np.linspace(1, 0, rate * 2)
    audio[-len(fade):] *= fade[:, None]
    with wave.open(str(path), "wb") as output:
        output.setparams((2, 2, rate, 0, "NONE", "not compressed"))
        output.writeframes((np.clip(audio, -1, 1) * 32767).astype("<i2").tobytes())

# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

"""Synthesize Mokka narration and derive caption cues."""

import re

from kokoro_onnx import Kokoro
import numpy as np
import onnxruntime as ort

from .prepare_assets import VOICE_ASSETS, check_asset


RATE = 24_000
VOICE, LANGUAGE = "am_puck", "en-us"
PRONUNCIATIONS = {
    "Mokka": "mocha",
    "CI/CD": "C I slash C D",
    "CI": "C I",
    "DRA": "D R A",
    "DCGM": "D C G M",
    "ResourceSlices": "resource slices",
    "MIG": "mig",
    "NVSentinel": "N V Sentinel",
    "nvidia-smi": "envidia SMI",
    "NVIDIA": "envidia",
}
TERMS = re.compile(r"\b(" + "|".join(re.escape(term) for term in PRONUNCIATIONS) + r")\b")


def voice_engine(cache):
    model, voices = (check_asset(cache / name, digest) for name, digest in VOICE_ASSETS.items())
    ort.disable_telemetry_events()
    options = ort.SessionOptions()
    options.intra_op_num_threads = 2
    options.inter_op_num_threads = 1
    session = ort.InferenceSession(str(model), sess_options=options, providers=["CPUExecutionProvider"])
    return Kokoro.from_session(session, str(voices))


def spoken_take(engine, text):
    spoken = TERMS.sub(lambda match: PRONUNCIATIONS[match[0]], text)
    phonemes = engine.tokenizer.phonemize(spoken, lang=LANGUAGE)
    phonemes = phonemes.replace("ɛnvˈɪdiəɹ", "ɛnvˈɪdiə")
    # Keep GB300 in one stress group so the model name flows within the sentence.
    phonemes = phonemes.replace("dʒˌiːbˈiː θɹˈiːhˈʌndɹɪd", "dʒˌiːbˌiː θɹˌiː hˈʌndɹɪd")
    samples, rate = engine.create(phonemes, voice=VOICE, speed=1.05, lang=LANGUAGE, is_phonemes=True)
    if rate != RATE or samples.ndim != 1 or not np.all(np.isfinite(samples)):
        raise ValueError(f"Invalid speech for {text!r}")
    activity = np.flatnonzero(np.abs(samples) > 0.005)
    if not len(activity):
        raise ValueError(f"Empty speech for {text!r}")
    take = samples[max(0, activity[0] - RATE // 25):activity[-1] + RATE // 16].copy()
    fade = np.linspace(0, 1, RATE // 100)
    take[:len(fade)] *= fade
    take[-len(fade):] *= fade[::-1]
    rms = np.sqrt(np.mean(take ** 2))
    take *= min(0.105 / rms, 0.77 / np.max(np.abs(take)))
    return take


def narration(engine, spec, edges):
    voice = np.zeros(round(edges[-1] * RATE), dtype=np.float32)
    captions = []
    gap = round(0.27 * RATE)
    for scene, end in zip(spec["scenes"], edges[1:]):
        start_sample = round((scene["start"] + 0.62) * RATE)
        for text in scene["voice"]:
            take = spoken_take(engine, text)
            stop_sample = start_sample + len(take)
            if stop_sample > (end - 0.35) * RATE:
                raise ValueError(f"Narration exceeds the scene ending at {end}s. Shorten {text!r}.")
            voice[start_sample:stop_sample] = take
            captions.append((start_sample / RATE - 0.05, stop_sample / RATE + 0.12, text))
            start_sample = stop_sample + gap
    return voice, captions

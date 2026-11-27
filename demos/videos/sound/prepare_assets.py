# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

"""Fetch and verify the local Kokoro assets."""

from hashlib import file_digest
import os
from pathlib import Path
import shutil
from tempfile import TemporaryDirectory
from urllib.request import urlopen


MODEL_URL = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/"
VOICE_ASSETS = {
    "kokoro-v1.0.int8.onnx": "6e742170d309016e5891a994e1ce1559c702a2ccd0075e67ef7157974f6406cb",
    "voices-v1.0.bin": "bca610b8308e8d99f32e6fe4197e7ec01679264efed0cac9140fe9c29f1fbf7d",
}


def check_asset(path, digest):
    with path.open("rb") as source:
        if file_digest(source, "sha256").hexdigest() != digest:
            raise ValueError(f"SHA-256 mismatch for {path}")
    return path


def prepare_assets(cache):
    cache.mkdir(parents=True, exist_ok=True)
    for name, digest in VOICE_ASSETS.items():
        path = cache / name
        if path.exists():
            check_asset(path, digest)
            continue
        print(f"Downloading {name}", flush=True)
        with TemporaryDirectory(dir=cache) as staging:
            download = Path(staging) / name
            with urlopen(MODEL_URL + name, timeout=45) as response, download.open("wb") as target:
                shutil.copyfileobj(response, target)
            check_asset(download, digest)
            os.link(download, path)

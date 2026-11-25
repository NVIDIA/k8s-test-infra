# Rebuild the Mokka video locally

See the [video index](README.md) for the episode overview.

## Rebuild the MP4

Use Python 3.12, Cairo with development headers, `pkg-config`, and FFmpeg with
`ffprobe` and `libx264`. On macOS, run
`brew install python@3.12 cairo pkg-config ffmpeg`.

Run these commands from the repository root.

```sh
video_work="$(mktemp -d)"
python3.12 -m venv "$video_work/venv"
video_python="$video_work/venv/bin/python"
export PYTHONDONTWRITEBYTECODE=1

"$video_python" -m pip install -r demos/videos/sound/requirements.txt
"$video_python" demos/videos/render.py --assets "$video_work/assets" --prepare-assets
"$video_python" demos/videos/render.py --assets "$video_work/assets" \
  --output dist/mokka-video/01-mokka-gpu-workflows-features-v2.mp4
```

Asset preparation downloads and verifies the Kokoro v1.0 model and voice bank.
The renderer uses the local Puck voice (`am_puck`) and times the burned-in
captions to the narration. It verifies the 1080p, 30 fps MP4 before writing it.
Use a fresh output path for revisions.

## Edit the episode

As the series grows, group new episodes in the [video index](README.md)
under headings such as `## Core workflow` or `## Core features`.

- [Episode JSON](episodes/01-mokka-gpu-workflows.json) holds scene start times,
  narration, opening text, feature cards, and end-card destinations.
- [Artwork](artwork/__init__.py) lays out the slides and animates the robot.
- [Narration](sound/voice.py) controls pronunciation and caption timing.
  The [music source](sound/music.py) generates the backing score.
- The [quick-start capture](episodes/captures/nvidia-smi-L.txt) and
  [integration results](episodes/captures/integration-captures.json) supply the observed output.

### Mokka robot

The [robot sprite](artwork/mokka-robot.png) was created with built-in ImageGen using the
[Mokka logo](../../docs/img/logo.png) as its reference. The renderer animates
the mouth from the narration audio and adds a blink and a gentle bob in the
six middle scenes. The opening slide and end card use the original logo.

<details>
<summary>ImageGen prompt</summary>

Use case: illustration-story. Create one transparent PNG sprite for the Mokka Kubernetes project video, transforming the supplied Mokka logo into a cute, friendly talking robot presenter. Preserve its recognizable white ceramic coffee cup and handle, brown coffee rim, blue Kubernetes wheel-shaped base, graphite GPU and lime-green detail, and white steam curls. Make the GPU a full front-facing robot head popping out of the mug at center, with softened black/charcoal corners, big round warm lime LED eyes, and the shape of a graphics card still legible. Add tiny rounded robot arms and mitten hands at the sides of the cup, one in a small friendly wave, and a playful compact silhouette. Clean polished editorial cartoon-style sticker with bold edges and modest shading like the reference. Crucial animation requirement: perfectly front-facing near-flat opaque dark charcoal screen on the GPU head, with both eyes across its upper half and generous uninterrupted dark space across its lower half for a mouth to be drawn during animation. The face can have a very tiny closed smile in that dark lower half, with no teeth or tongue. At small size the eyes, cup and blue base must still read clearly. Single character, centered with even tight padding, all parts inside the square, true transparent alpha background, no backdrop or floor shadow, no text, no letters, no other logos, no watermark. This is one complete presenter asset, not a sprite sheet.

</details>

## Capture the terminal output

Run against a disposable Kind cluster on a host that does not pass through
GPU devices. The original capture used chart `0.7.0`, the default `gb300`
profile, and a single Linux ARM64 Kind node. Use a separate kubeconfig to
keep the capture on that cluster.

```sh
capture_work="$(mktemp -d)"
(
  set -e
  export KUBECONFIG="$capture_work/kubeconfig"
  kind create cluster --name mokka-video --kubeconfig "$KUBECONFIG"
  helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
    --version 0.7.0 --namespace mokka --create-namespace --wait --timeout 8m
  kubectl rollout status -n mokka ds/nvml-mock --timeout=180s
  kubectl exec -n mokka ds/nvml-mock -- nvidia-smi -L > "$capture_work/nvidia-smi-L.txt"
  kind delete cluster --name mokka-video --kubeconfig "$KUBECONFIG"
)
cmp demos/videos/episodes/captures/nvidia-smi-L.txt "$capture_work/nvidia-smi-L.txt"
```

## Capture the integration results

Use a separate cluster and kubeconfig for each integration. Both captures used
`kindest/node:v1.35.0` on ARM64 and Mokka chart `0.7.0` with the default
`gb300` profile and `featureGates.IMEXDownload=false`.

- **GPU Operator.** Follow the [v0.7.0 GPU Operator guide](https://github.com/NVIDIA/k8s-test-infra/blob/v0.7.0/docs/guides/gpu-operator.md)
  with GPU Operator `v26.3.3` and its Mokka values overlay. After the device
  plugin, GPU Feature Discovery, exporter, and validator are ready, read
  `nvidia.com/gpu` from the node's allocatable resources and
  `nvidia.com/gpu.product` from its labels. Scrape the exporter's `/metrics`
  endpoint on port 9400 and record `DCGM_FI_DEV_GPU_TEMP` for `gpu="0"`.
- **DRA.** Follow the [v0.7.0 DRA guide](https://github.com/NVIDIA/k8s-test-infra/blob/v0.7.0/docs/guides/dra.md)
  with NVIDIA DRA Driver `0.5.0` and the guide's DRA-enabled Kind config.
  Count the published GPUs, wait for `gpu-test-pod` to reach `Running`,
  and resolve the claim's allocated device to its ResourceSlice UUID.
  Compare that UUID with the single line from
  `kubectl exec gpu-test-pod -- nvidia-smi -L`.

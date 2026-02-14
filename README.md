# Cloud Native Test Infrastructure — Dashboard

A React SPA dashboard and project portfolio for NVIDIA's cloud-native Kubernetes test infrastructure.

Live at: https://nvidia.github.io/k8s-test-infra/

## Features

- **E2E Test Dashboard** — Live test results from Ginkgo JSON artifacts (holodeck, container-toolkit, device-plugin)
- **Workflow Status** — Latest CI status for all 9 NVIDIA cloud-native repos
- **Image Builds** — Latest container image tags and push timestamps
- **Project Catalog** — Overview cards for all projects with descriptions and links

## Tech Stack

- [Vite](https://vitejs.dev/) + [React 19](https://react.dev/) + [TypeScript](https://www.typescriptlang.org/)
- [Tailwind CSS v4](https://tailwindcss.com/)
- [React Router v7](https://reactrouter.com/)
- [Lucide React](https://lucide.dev/) icons

## Development

```bash
npm install
npm run dev
```

Open http://localhost:5173/k8s-test-infra/

## Build

```bash
npm run build
```

Output in `dist/`.

## Data Pipeline

`artifact_fetcher.go` runs at build time in CI (every 6 hours) and produces:

- `public/data/results.json` — Ginkgo E2E test results
- `public/data/workflows.json` — Latest workflow run statuses
- `public/data/images.json` — Latest container image builds

## License

Apache License 2.0 — see [LICENSE](LICENSE).

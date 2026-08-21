# Cloud Native Test Infrastructure Dashboard (archived)

**This branch is an archive.** `gh-pages` holds the retired React CI dashboard,
kept so the code and its history stay readable. It is not built, not deployed,
and not maintained.

**Live documentation: <https://nvidia.github.io/k8s-test-infra/>**, served by
GitHub Pages from the `main` branch and built by
`.github/workflows/deploy-pages.yaml`. It has nothing to do with this branch.

The Pages deployment workflow here, `.github/workflows/deploy.yaml`, has been
disabled and has no trigger left. `actions/deploy-pages` replaces the whole
Pages site, so a single run of it would overwrite the live documentation with
this old dashboard. See issue #696, and read the comment at the top of that
file before changing anything in it.

Everything below describes the dashboard as it stood when it was retired, and
is kept for reference only.

A React SPA dashboard and project portfolio for NVIDIA's cloud-native Kubernetes test infrastructure.

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

`artifact_fetcher.go` ran at build time in CI, while the deployment workflow was
still enabled, and produced:

- `public/data/results.json` — Ginkgo E2E test results
- `public/data/workflows.json` — Latest workflow run statuses
- `public/data/images.json` — Latest container image builds

## License

Apache License 2.0 — see [LICENSE](LICENSE).

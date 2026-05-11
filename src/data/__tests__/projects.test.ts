import { describe, it, expect } from 'vitest';
import { projects } from '../projects';

// Guards the React-side half of the DRA repo migration:
// NVIDIA donated k8s-dra-driver-gpu to kubernetes-sigs on 2026-04-30.
// The entry's slug ('k8s-dra-driver-gpu') and name ('K8s DRA Driver GPU')
// stay stable so external bookmarks survive and the user-facing label
// doesn't churn; only the repo literal moves.
//
// Mutation check: reverting `repo` back to 'NVIDIA/k8s-dra-driver-gpu'
// fails this test. Task 11 broadens this into a static-shape test that
// also reads the Go side's defaultRepos / allRepos / defaultImages and
// asserts the four literals stay aligned.
//
// Spec: docs/plans/2026-04-30-dra-repo-migration-design.md (folded into
// PR-B per F2).
describe('projects DRA entry', () => {
  const dra = projects.find((p) => p.slug === 'k8s-dra-driver-gpu');

  it('exists (slug must remain stable for external bookmarks)', () => {
    expect(dra).toBeDefined();
  });

  it('keeps the human-facing name unchanged after migration', () => {
    expect(dra?.name).toBe('K8s DRA Driver GPU');
  });

  it('uses the kubernetes-sigs canonical repo path', () => {
    expect(dra?.repo).toBe('kubernetes-sigs/dra-driver-nvidia-gpu');
  });

  it('does not reference the donated-away NVIDIA path', () => {
    expect(dra?.repo).not.toBe('NVIDIA/k8s-dra-driver-gpu');
    expect(dra?.repo?.toLowerCase()).not.toContain('nvidia/');
  });
});

import { describe, it, expect, beforeAll } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import IssuesPRsDashboard from '../IssuesPRsDashboard';
import ThemeProvider from '../ThemeProvider';
import type { IssuesPRsData } from '../../types';

// jsdom 28 + vitest 4: window.localStorage is not auto-instantiated for
// the default about:blank URL, and matchMedia is famously not implemented.
// ThemeProvider touches both on mount, so install minimal in-memory shims.
beforeAll(() => {
  if (typeof globalThis.localStorage === 'undefined' ||
      typeof globalThis.localStorage.getItem !== 'function') {
    const store = new Map<string, string>();
    const stub: Storage = {
      get length() { return store.size; },
      clear: () => store.clear(),
      getItem: (k) => (store.has(k) ? store.get(k)! : null),
      key: (i) => Array.from(store.keys())[i] ?? null,
      removeItem: (k) => { store.delete(k); },
      setItem: (k, v) => { store.set(k, String(v)); },
    };
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: stub,
    });
  }
  if (typeof window.matchMedia !== 'function') {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: (query: string): MediaQueryList => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  }
});

// Build distinguishable fixture data so daily and weekly slices yield
// non-overlapping values — required for the snapshot-regression test
// to detect filtering through pickVelocity.
function makeData(): IssuesPRsData {
  return {
    repos: {
      'nvidia/gpu-operator': {
        fetchedAt: '2026-04-30T12:00:00Z',
        issues: {
          total: 42,
          categories: { bug: 12, 'feature-request': 8 },
          ageBuckets: { fresh: 5, recent: 10, aging: 12, stale: 8, ancient: 7 },
          velocity: {
            daily: Array.from({ length: 365 }, (_, i) => ({
              date: `2026-day-${i}`,
              opened: 1000 + i,
              closed: 500 + i,
            })),
            weekly: Array.from({ length: 260 }, (_, i) => ({
              week: `wk-${i}`,
              opened: i,
              closed: 500 - i,
            })),
          },
        },
        pullRequests: {
          total: 8,
          categories: {},
          ageBuckets: { fresh: 3, recent: 2, aging: 2, stale: 1, ancient: 0 },
          velocity: {
            daily: Array.from({ length: 365 }, (_, i) => ({
              date: `2026-day-${i}`,
              opened: 0,
              closed: 200 + i,
            })),
            weekly: Array.from({ length: 260 }, (_, i) => ({
              week: `wk-${i}`,
              opened: 0,
              closed: 200 - i,
            })),
          },
          review: { awaitingReview: 3, noReviewer: 1, avgDaysToFirstReview: 1.5, avgDaysToMerge: 3.2 },
        },
      },
    },
  };
}

function renderDashboard(data: IssuesPRsData) {
  return render(
    <MemoryRouter>
      <ThemeProvider>
        <IssuesPRsDashboard data={data} />
      </ThemeProvider>
    </MemoryRouter>,
  );
}

describe('IssuesPRsDashboard', () => {
  it('defaults to the 12w duration', () => {
    renderDashboard(makeData());
    const btn = screen.getByRole('button', { name: /Show 12w of velocity data/ });
    expect(btn).toHaveAttribute('aria-pressed', 'true');
  });

  it('renders all 6 duration buttons in display order', () => {
    renderDashboard(makeData());
    const labels = ['7d', '4w', '12w', '6m', '1y', '5y'];
    for (const label of labels) {
      expect(screen.getByRole('button', { name: new RegExp(`Show ${label} of velocity data`) }))
        .toBeInTheDocument();
    }
  });

  it('clicking a duration button updates aria-pressed', async () => {
    const user = userEvent.setup();
    renderDashboard(makeData());

    const btn7d = screen.getByRole('button', { name: /Show 7d of velocity data/ });
    expect(btn7d).toHaveAttribute('aria-pressed', 'false');

    await user.click(btn7d);

    expect(btn7d).toHaveAttribute('aria-pressed', 'true');
    const btn12w = screen.getByRole('button', { name: /Show 12w of velocity data/ });
    expect(btn12w).toHaveAttribute('aria-pressed', 'false');
  });

  // Q3 invariant (snapshot regression test, strengthened per QA review).
  // Uses stable test IDs (not text content) so a regression can't pass by
  // coincidence with another element rendering the same number — e.g. the
  // categories bar chart can render '8' for the feature-request count,
  // which would collide with pullRequests.total === 8 if querying by text.
  it('snapshot stats do not change when duration changes (Q3 invariant)', async () => {
    const user = userEvent.setup();
    renderDashboard(makeData());

    const openIssuesCell = screen.getByTestId('open-issues-gpu-operator');
    const openPRsCell = screen.getByTestId('open-prs-gpu-operator');
    const initialIssuesText = openIssuesCell.textContent;
    const initialPRsText = openPRsCell.textContent;

    expect(initialIssuesText).toBe('42');
    expect(initialPRsText).toBe('8');

    for (const label of ['7d', '4w', '12w', '6m', '1y', '5y']) {
      const btn = screen.getByRole('button', { name: new RegExp(`Show ${label} of velocity data`) });
      await user.click(btn);

      expect(openIssuesCell.textContent).toBe(initialIssuesText);
      expect(openPRsCell.textContent).toBe(initialPRsText);
    }
  });
});

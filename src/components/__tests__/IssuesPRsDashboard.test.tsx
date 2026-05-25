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

// Variant of makeData() where weekly velocity starts on a known ISO date so
// the clamp branch in pickVelocity fires when `from` predates that week.
function makeDataWithWeeklyStart(firstWeek: string): IssuesPRsData {
  const base = makeData();
  const repoData = base.repos['nvidia/gpu-operator'];
  // Replace the first weekly entry's key with the given ISO date so
  // pickVelocity's `earliest = weekly[0].week` comparison uses a real date.
  const issueWeekly = [
    { week: firstWeek, opened: 0, closed: 0 },
    ...repoData.issues.velocity.weekly.slice(1),
  ];
  const prWeekly = [
    { week: firstWeek, opened: 0, closed: 0 },
    ...repoData.pullRequests.velocity.weekly.slice(1),
  ];
  return {
    repos: {
      'nvidia/gpu-operator': {
        ...repoData,
        issues: {
          ...repoData.issues,
          velocity: { ...repoData.issues.velocity, weekly: issueWeekly },
        },
        pullRequests: {
          ...repoData.pullRequests,
          velocity: { ...repoData.pullRequests.velocity, weekly: prWeekly },
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
    // DurationPicker trigger shows the current range label
    expect(screen.getByRole('button', { name: /range/i })).toHaveTextContent('Last 12 weeks');
  });

  it('renders the DurationPicker trigger showing all 6 presets via the popover', async () => {
    const user = userEvent.setup();
    renderDashboard(makeData());

    await user.click(screen.getByRole('button', { name: /range/i }));

    const expectedLabels = [
      'Last 7 days',
      'Last 4 weeks',
      'Last 12 weeks',
      'Last 6 months',
      'Last 1 year',
      'Last 5 years',
    ];
    for (const label of expectedLabels) {
      expect(screen.getByRole('menuitem', { name: label })).toBeInTheDocument();
    }
  });

  it('selecting a preset from DurationPicker updates the trigger label', async () => {
    const user = userEvent.setup();
    renderDashboard(makeData());

    const trigger = screen.getByRole('button', { name: /range/i });
    expect(trigger).toHaveTextContent('Last 12 weeks');

    await user.click(trigger);
    await user.click(screen.getByRole('menuitem', { name: 'Last 7 days' }));

    expect(screen.getByRole('button', { name: /range/i })).toHaveTextContent('Last 7 days');
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

    const presetLabels = [
      'Last 7 days',
      'Last 4 weeks',
      'Last 12 weeks',
      'Last 6 months',
      'Last 1 year',
      'Last 5 years',
    ];
    for (const label of presetLabels) {
      await user.click(screen.getByRole('button', { name: /range/i }));
      await user.click(screen.getByRole('menuitem', { name: label }));

      expect(openIssuesCell.textContent).toBe(initialIssuesText);
      expect(openPRsCell.textContent).toBe(initialPRsText);
    }
  });

  it('renders clamp notes when a custom range predates the weekly retention', async () => {
    const user = userEvent.setup();
    // Weekly data starts at 2024-01-01; requesting from 2020-06-01 triggers clamping.
    const data = makeDataWithWeeklyStart('2024-01-01');
    renderDashboard(data);

    // Expand the GPU Operator row to reveal the velocity charts.
    // The row has role="button" and its accessible name includes "GPU Operator".
    await user.click(screen.getByRole('button', { name: /GPU Operator/i }));

    // Open the DurationPicker, select Custom range, enter clamping dates, apply.
    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));
    await user.type(screen.getByLabelText('From'), '2020-06-01');
    await user.type(screen.getByLabelText('To'), '2024-06-30');
    await user.click(screen.getByRole('button', { name: 'Apply' }));

    // Both clamp notes must appear with the corrected wording.
    const issueNote = await screen.findByTestId('issue-clamp-note');
    const prNote = await screen.findByTestId('pr-clamp-note');
    expect(issueNote).toHaveTextContent('Showing data from 2024-01-01');
    expect(issueNote).toHaveTextContent('(requested 2020-06-01)');
    expect(prNote).toHaveTextContent('Showing data from 2024-01-01');
    expect(prNote).toHaveTextContent('(requested 2020-06-01)');
    // ARIA: screen readers must announce the clamp note when it appears
    // after a range change. Removing aria-live would silently degrade SR UX.
    expect(issueNote).toHaveAttribute('aria-live', 'polite');
    expect(prNote).toHaveAttribute('aria-live', 'polite');
  });
});

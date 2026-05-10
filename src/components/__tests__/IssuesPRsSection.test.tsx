import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import IssuesPRsSection from '../IssuesPRsSection';
import type { RepoIssuesPRs } from '../../types';

// useTheme is invoked at component-mount; stub it instead of wrapping in
// ThemeProvider so the test stays focused on velocity rendering.
vi.mock('../ThemeProvider', () => ({
  useTheme: () => ({ resolved: 'light' }),
}));

// Mock recharts so the LineChart's data prop is exposed as a queryable
// DOM attribute. The bar chart used for ageBuckets is rendered via the
// same module mock; only the velocity LineChart is asserted on here.
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  LineChart: ({ data, children }: { data: unknown[]; children: React.ReactNode }) => (
    <div data-testid="velocity-line-chart" data-points={JSON.stringify(data)}>{children}</div>
  ),
  Line: () => null,
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  Cell: () => null,
  XAxis: () => null,
  YAxis: () => null,
  CartesianGrid: () => null,
  Tooltip: () => null,
  Legend: () => null,
}));

interface ChartPoint {
  week: string;
  opened: number;
  closed: number;
}

function readVelocityChartData(): ChartPoint[] {
  const raw = screen.getByTestId('velocity-line-chart').getAttribute('data-points');
  if (!raw) throw new Error('velocity-line-chart has no data-points attribute');
  return JSON.parse(raw) as ChartPoint[];
}

function makeFixture(weeks: number): RepoIssuesPRs {
  const buildVelocity = (basis: number) => ({
    daily: [],
    weekly: Array.from({ length: weeks }, (_, i) => ({
      week: `W${String(i).padStart(3, '0')}`,
      opened: basis + i,
      closed: basis - i,
    })),
  });
  const emptyAge = { fresh: 0, recent: 0, aging: 0, stale: 0, ancient: 0 };
  return {
    fetchedAt: '2026-05-10T00:00:00Z',
    issues: {
      total: 1,
      ageBuckets: emptyAge,
      categories: {},
      velocity: buildVelocity(100),
    },
    pullRequests: {
      total: 1,
      ageBuckets: emptyAge,
      categories: {},
      velocity: buildVelocity(200),
      review: {
        awaitingReview: 0,
        noReviewer: 0,
        avgDaysToFirstReview: 0,
        avgDaysToMerge: 0,
      },
    },
  };
}

describe('IssuesPRsSection velocity chart', () => {
  it('renders only the last 12 weeks when the fixture carries more', () => {
    const data = makeFixture(260);
    render(<IssuesPRsSection data={data} />);

    const points = readVelocityChartData();
    expect(points).toHaveLength(12);
    // Last 12 of 260 weeks: indices 248..259
    expect(points[0]?.week).toBe('W248');
    expect(points[11]?.week).toBe('W259');
  });

  it('renders all weeks when fewer than 12 are available', () => {
    const data = makeFixture(5);
    render(<IssuesPRsSection data={data} />);

    const points = readVelocityChartData();
    expect(points).toHaveLength(5);
    expect(points[0]?.week).toBe('W000');
    expect(points[4]?.week).toBe('W004');
  });
});

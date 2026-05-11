import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import VelocitySparkline from '../VelocitySparkline';
import type { Velocity } from '../../types';

// Mock recharts so the data prop passed into <LineChart> is exposed as a
// queryable DOM attribute. This lets us assert the actual slice/granularity
// chosen by pickVelocity rather than mere container presence.
//
// vi.mock is hoisted by vitest, so this is in place before VelocitySparkline
// imports recharts.
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  LineChart: ({ data, children }: { data: unknown[]; children: React.ReactNode }) => (
    <div data-testid="line-chart-mock" data-points={JSON.stringify(data)}>{children}</div>
  ),
  Line: () => null,
}));

interface ChartPoint {
  label: string;
  opened: number;
  closed: number;
}

function readChartData(): ChartPoint[] {
  const raw = screen.getByTestId('line-chart-mock').getAttribute('data-points');
  if (!raw) throw new Error('line-chart-mock has no data-points attribute');
  return JSON.parse(raw) as ChartPoint[];
}

function makeFixture(): Velocity {
  // Daily and weekly opened/closed ranges are intentionally disjoint so a
  // wrong-source regression (e.g. weekly returned for 7d) is unambiguous.
  const daily = Array.from({ length: 365 }, (_, i) => ({
    date: `D${i}`,
    opened: 10000 + i, // 10000..10364
    closed: 1000 + i,  // 1000..1364 — distinct from opened, guards `closed` series
  }));
  const weekly = Array.from({ length: 260 }, (_, i) => ({
    week: `W${i}`,
    opened: 100 + i,   // 100..359 — disjoint from daily.opened range
    closed: 50 + i,    // 50..309
  }));
  return { daily, weekly };
}

describe('VelocitySparkline', () => {
  it('renders for 7d using daily data', () => {
    const v = makeFixture();
    render(<VelocitySparkline velocity={v} duration="7d" />);

    const data = readChartData();
    // Length: pickVelocity('7d') slices daily by -7.
    expect(data).toHaveLength(7);
    // Last 7 of daily (length 365) is indices 358..364.
    expect(data[0].label).toBe('D358');
    expect(data[6].label).toBe('D364');
    // Source: opened values must come from daily (10000-range), not weekly (100-range).
    // If pickVelocity wrongly returned weekly for 7d, opened would be in 100..359.
    expect(data[0].opened).toBe(10358);
    expect(data[6].opened).toBe(10364);
    // Closed series must also come from daily (1000-range), not weekly (50-range).
    expect(data[0].closed).toBe(1358);
  });

  it('renders for 5y using weekly data', () => {
    const v = makeFixture();
    render(<VelocitySparkline velocity={v} duration="5y" />);

    const data = readChartData();
    // Length: pickVelocity('5y') slices weekly by -260; weekly is exactly 260 long.
    expect(data).toHaveLength(260);
    // slice(-260) on a 260-length array is the whole array.
    expect(data[0].label).toBe('W0');
    expect(data[259].label).toBe('W259');
    // Source: opened values must come from weekly (100-range), not daily (10000-range).
    // If pickVelocity wrongly returned daily for 5y, opened would be in 10000..10364
    // and length would be 7, not 260.
    expect(data[0].opened).toBe(100);
    expect(data[259].opened).toBe(359);
    // Closed series must come from weekly (50-range).
    expect(data[0].closed).toBe(50);
  });

  it('renders the empty state when both arrays are empty', () => {
    render(<VelocitySparkline velocity={{ daily: [], weekly: [] }} duration="7d" />);
    expect(screen.getByTestId('sparkline-empty')).toBeInTheDocument();
  });
});

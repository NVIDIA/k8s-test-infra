import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, it, expect, vi } from 'vitest';
import type { WorkflowStatus } from '../../types';

// Generate N workflow entries for pagination testing
function makeWorkflows(n: number): WorkflowStatus[] {
  return Array.from({ length: n }, (_, i) => ({
    repo: 'test-repo',
    workflow: `workflow-${String(i + 1).padStart(3, '0')}`,
    status: 'success' as const,
    conclusion: 'success',
    runUrl: `https://example.com/run/${i}`,
    updatedAt: new Date(Date.now() - i * 60000).toISOString(),
    commitSha: `abc${String(i).padStart(4, '0')}0000000000000000000000000000000000`,
    commitUrl: `https://example.com/commit/${i}`,
  }));
}

// Mock all data hooks
vi.mock('../../hooks/useData', () => ({
  useTestResults: () => ({ data: [], loading: false, error: null }),
  useWorkflowStatuses: () => ({
    data: makeWorkflows(25),
    loading: false,
    error: null,
  }),
  useImageBuilds: () => ({ data: [], loading: false, error: null }),
  useHistory: () => ({ data: null, loading: false, error: null }),
  useIssuesPRs: () => ({ data: null, loading: false, error: null }),
}));

// Mock recharts to avoid SVG rendering issues in jsdom
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => children,
  AreaChart: () => null,
  Area: () => null,
  XAxis: () => null,
  YAxis: () => null,
  CartesianGrid: () => null,
  Tooltip: () => null,
  Legend: () => null,
}));

import Dashboard from '../Dashboard';

function renderDashboard() {
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );
}

describe('Dashboard Workflow Status pagination', () => {
  it('renders only the first page of workflows (20 of 25)', () => {
    renderDashboard();
    // With 25 workflows and PAGE_SIZE=20, only 20 should be visible
    const heading = screen.getAllByText('Workflow Status').find(el => el.tagName === 'H2') as HTMLElement;
    const workflowSection = heading.closest('section') as HTMLElement;
    const rows = workflowSection.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(20);
  });

  it('shows pagination controls for workflows', () => {
    renderDashboard();
    // Should show "Page 1 of 2" (25 items / 20 per page = 2 pages)
    expect(screen.getByText('Page 1 of 2')).toBeInTheDocument();
  });

  it('does not render workflow-021 on the first page', () => {
    renderDashboard();
    // workflow-021 through workflow-025 should be on page 2
    expect(screen.queryByText('workflow-021')).not.toBeInTheDocument();
  });
});

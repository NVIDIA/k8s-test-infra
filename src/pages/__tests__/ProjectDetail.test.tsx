import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, it, expect, vi } from 'vitest';
import type { WorkflowStatus } from '../../types';

const TEST_REPO = 'nvidia/gpu-operator';

// Generate N workflow entries for a specific repo
function makeWorkflows(n: number, repo: string = TEST_REPO): WorkflowStatus[] {
  return Array.from({ length: n }, (_, i) => ({
    repo,
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
  useRepoInfos: () => ({ data: [], loading: false, error: null }),
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

import ProjectDetail from '../ProjectDetail';

function renderProjectDetail() {
  return render(
    <MemoryRouter initialEntries={['/projects/gpu-operator']}>
      <Routes>
        <Route path="/projects/:slug" element={<ProjectDetail />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ProjectDetail CI Status pagination', () => {
  it('renders only the first page of workflows (20 of 25)', () => {
    renderProjectDetail();
    const heading = screen.getAllByText('CI Status').find(el => el.tagName === 'H2') as HTMLElement;
    const ciSection = heading.closest('section') as HTMLElement;
    const rows = ciSection.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(20);
  });

  it('shows pagination controls', () => {
    renderProjectDetail();
    // 25 items / 20 per page = 2 pages
    expect(screen.getByText('Page 1 of 2')).toBeInTheDocument();
  });

  it('does not render workflow-021 on the first page', () => {
    renderProjectDetail();
    expect(screen.queryByText('workflow-021')).not.toBeInTheDocument();
  });
});

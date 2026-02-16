import { useState, useMemo } from 'react';
import { ExternalLink, Search } from 'lucide-react';
import Layout from '../components/Layout';
import StatusBadge from '../components/StatusBadge';
import TrendChart from '../components/TrendChart';
import { useTestResults, useWorkflowStatuses, useImageBuilds, useHistory, useIssuesPRs } from '../hooks/useData';
import IssuesPRsDashboard from '../components/IssuesPRsDashboard';

const sidebarItems = [
  { to: '/dashboard', label: 'Overview' },
  { to: '/dashboard#trends', label: 'Trends' },
  { to: '/dashboard#e2e-results', label: 'E2E Test Results' },
  { to: '/dashboard#workflow-status', label: 'Workflow Status' },
  { to: '/dashboard#issues-prs', label: 'Issues & PRs' },
  { to: '/dashboard#image-builds', label: 'Image Builds' },
];

function FilterBar({
  repos,
  selectedRepo,
  onRepoChange,
  statuses,
  selectedStatus,
  onStatusChange,
  searchTerm,
  onSearchChange,
}: {
  repos: string[];
  selectedRepo: string;
  onRepoChange: (v: string) => void;
  statuses?: string[];
  selectedStatus?: string;
  onStatusChange?: (v: string) => void;
  searchTerm: string;
  onSearchChange: (v: string) => void;
}) {
  return (
    <div className="flex flex-col sm:flex-row gap-2 mb-3">
      <div className="relative flex-1">
        <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500" />
        <input
          type="text"
          placeholder="Search..."
          value={searchTerm}
          onChange={(e) => onSearchChange(e.target.value)}
          className="w-full pl-8 pr-3 py-1.5 text-sm border border-gray-300 dark:border-gray-600 rounded-md focus:outline-none focus:ring-1 focus:ring-nvidia-green dark:bg-gray-700 dark:text-gray-200"
        />
      </div>
      <select
        value={selectedRepo}
        onChange={(e) => onRepoChange(e.target.value)}
        className="px-3 py-1.5 text-sm border border-gray-300 dark:border-gray-600 rounded-md focus:outline-none focus:ring-1 focus:ring-nvidia-green dark:bg-gray-700 dark:text-gray-200"
      >
        <option value="">All repos</option>
        {repos.map((r) => (
          <option key={r} value={r}>{r}</option>
        ))}
      </select>
      {statuses && onStatusChange && (
        <select
          value={selectedStatus}
          onChange={(e) => onStatusChange(e.target.value)}
          className="px-3 py-1.5 text-sm border border-gray-300 dark:border-gray-600 rounded-md focus:outline-none focus:ring-1 focus:ring-nvidia-green dark:bg-gray-700 dark:text-gray-200"
        >
          <option value="">All statuses</option>
          {statuses.map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
      )}
    </div>
  );
}

export default function Dashboard() {
  const { data: results, loading: resultsLoading } = useTestResults();
  const { data: workflows, loading: workflowsLoading } = useWorkflowStatuses();
  const { data: images, loading: imagesLoading } = useImageBuilds();
  const { data: history } = useHistory();
  const { data: issuesPRs } = useIssuesPRs();
  const [trendRange, setTrendRange] = useState<7 | 30 | 90>(7);

  const trendData = useMemo(() => {
    if (!history) return [];
    const cutoff = Date.now() - trendRange * 24 * 60 * 60 * 1000;
    return history.snapshots
      .filter((s) => new Date(s.timestamp).getTime() >= cutoff)
      .map((s) => ({
        date: s.timestamp,
        success: s.workflows['success'] ?? 0,
        failure: s.workflows['failure'] ?? 0,
      }));
  }, [history, trendRange]);

  // Results filters
  const [resultsRepo, setResultsRepo] = useState('');
  const [resultsSearch, setResultsSearch] = useState('');

  // Workflow filters
  const [wfRepo, setWfRepo] = useState('');
  const [wfStatus, setWfStatus] = useState('');
  const [wfSearch, setWfSearch] = useState('');

  // Image filters
  const [imgRepo, setImgRepo] = useState('');
  const [imgSearch, setImgSearch] = useState('');

  const resultRepos = useMemo(() => [...new Set(results.map((r) => r.repo))].sort(), [results]);
  const wfRepos = useMemo(() => [...new Set(workflows.map((w) => w.repo))].sort(), [workflows]);
  const wfStatuses = useMemo(() => [...new Set(workflows.map((w) => w.status))].sort(), [workflows]);
  const imgRepos = useMemo(() => [...new Set(images.map((i) => i.repo))].sort(), [images]);

  const filteredResults = useMemo(() => {
    return results.filter((r) => {
      if (resultsRepo && r.repo !== resultsRepo) return false;
      if (resultsSearch) {
        const q = resultsSearch.toLowerCase();
        return r.project.toLowerCase().includes(q) || r.repo.toLowerCase().includes(q);
      }
      return true;
    });
  }, [results, resultsRepo, resultsSearch]);

  const filteredWorkflows = useMemo(() => {
    return workflows.filter((w) => {
      if (wfRepo && w.repo !== wfRepo) return false;
      if (wfStatus && w.status !== wfStatus) return false;
      if (wfSearch) {
        const q = wfSearch.toLowerCase();
        return w.repo.toLowerCase().includes(q) || w.workflow.toLowerCase().includes(q);
      }
      return true;
    });
  }, [workflows, wfRepo, wfStatus, wfSearch]);

  const filteredImages = useMemo(() => {
    return images.filter((img) => {
      if (imgRepo && img.repo !== imgRepo) return false;
      if (imgSearch) {
        const q = imgSearch.toLowerCase();
        return img.repo.toLowerCase().includes(q) || img.tag.toLowerCase().includes(q);
      }
      return true;
    });
  }, [images, imgRepo, imgSearch]);

  return (
    <Layout sidebarItems={sidebarItems} sidebarTitle="Dashboard">
      <h1 className="text-2xl font-bold text-gray-900 dark:text-white mb-6">Dashboard</h1>

      <section id="trends" className="mb-8">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200">
            Workflow Trends
          </h2>
          <div className="flex gap-1">
            {([7, 30, 90] as const).map((d) => (
              <button
                key={d}
                onClick={() => setTrendRange(d)}
                className={`px-3 py-1 text-xs rounded-md font-medium transition-colors ${
                  trendRange === d
                    ? 'bg-nvidia-green text-white'
                    : 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300 hover:bg-gray-200 dark:hover:bg-gray-600'
                }`}
              >
                {d}d
              </button>
            ))}
          </div>
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <TrendChart
            data={trendData}
            areas={[
              { key: 'success', color: '#22c55e', name: 'Success' },
              { key: 'failure', color: '#ef4444', name: 'Failure' },
            ]}
            height={250}
            stacked
          />
        </div>
      </section>

      {/* E2E Test Results */}
      <section id="e2e-results" className="mb-8">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">E2E Test Results</h2>
        {!resultsLoading && (
          <FilterBar
            repos={resultRepos}
            selectedRepo={resultsRepo}
            onRepoChange={setResultsRepo}
            searchTerm={resultsSearch}
            onSearchChange={setResultsSearch}
          />
        )}
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
          {resultsLoading ? (
            <p className="p-4 text-gray-500 dark:text-gray-400">Loading...</p>
          ) : filteredResults.length === 0 ? (
            <p className="p-4 text-gray-500 dark:text-gray-400">No test results available.</p>
          ) : (
            <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Project</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Last Run</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Passed</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Failed</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Source</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Run</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {filteredResults.map((r) => (
                  <tr key={`${r.repo}-${r.project}`}>
                    <td className="px-4 py-3 text-sm text-gray-900 dark:text-white">{r.project}</td>
                    <td className="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{r.lastRun}</td>
                    <td className="px-4 py-3 text-sm text-status-pass font-medium">{r.passed}</td>
                    <td className="px-4 py-3 text-sm text-status-fail font-medium">{r.failed}</td>
                    <td className="px-4 py-3">
                      <span className="inline-block rounded bg-gray-100 dark:bg-gray-700 px-2 py-0.5 text-xs text-gray-600 dark:text-gray-400">
                        {r.source}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <a
                        href={r.actionRunUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-nvidia-green hover:text-nvidia-green-dark inline-flex items-center gap-1"
                      >
                        <ExternalLink size={14} />
                      </a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      {/* Workflow Status */}
      <section id="workflow-status" className="mb-8">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">Workflow Status</h2>
        {!workflowsLoading && (
          <FilterBar
            repos={wfRepos}
            selectedRepo={wfRepo}
            onRepoChange={setWfRepo}
            statuses={wfStatuses}
            selectedStatus={wfStatus}
            onStatusChange={setWfStatus}
            searchTerm={wfSearch}
            onSearchChange={setWfSearch}
          />
        )}
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
          {workflowsLoading ? (
            <p className="p-4 text-gray-500 dark:text-gray-400">Loading...</p>
          ) : filteredWorkflows.length === 0 ? (
            <p className="p-4 text-gray-500 dark:text-gray-400">No workflows match the current filters.</p>
          ) : (
            <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Repo</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Workflow</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Status</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Updated</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Commit</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Run</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {filteredWorkflows.map((w) => (
                  <tr key={`${w.repo}-${w.workflow}`}>
                    <td className="px-4 py-3 text-sm text-gray-900 dark:text-white">{w.repo}</td>
                    <td className="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">{w.workflow}</td>
                    <td className="px-4 py-3">
                      <StatusBadge status={w.status} />
                    </td>
                    <td className="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{w.updatedAt}</td>
                    <td className="px-4 py-3 text-sm">
                      <a
                        href={w.commitUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-nvidia-green hover:text-nvidia-green-dark font-mono text-xs"
                      >
                        {w.commitSha.substring(0, 7)}
                      </a>
                    </td>
                    <td className="px-4 py-3">
                      <a
                        href={w.runUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-nvidia-green hover:text-nvidia-green-dark inline-flex items-center gap-1"
                      >
                        <ExternalLink size={14} />
                      </a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      {/* Issues & PR Health */}
      {issuesPRs && <IssuesPRsDashboard data={issuesPRs} />}

      {/* Latest Image Builds */}
      <section id="image-builds" className="mb-8">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">Latest Image Builds</h2>
        {!imagesLoading && (
          <FilterBar
            repos={imgRepos}
            selectedRepo={imgRepo}
            onRepoChange={setImgRepo}
            searchTerm={imgSearch}
            onSearchChange={setImgSearch}
          />
        )}
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
          {imagesLoading ? (
            <p className="p-4 text-gray-500 dark:text-gray-400">Loading...</p>
          ) : filteredImages.length === 0 ? (
            <p className="p-4 text-gray-500 dark:text-gray-400">No images match the current filters.</p>
          ) : (
            <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Repo</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Tag</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Pushed At</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {filteredImages.map((img) => (
                  <tr key={`${img.repo}-${img.tag}`}>
                    <td className="px-4 py-3 text-sm text-gray-900 dark:text-white">{img.repo}</td>
                    <td className="px-4 py-3 text-sm">
                      <a
                        href={img.htmlUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-nvidia-green hover:text-nvidia-green-dark"
                      >
                        {img.tag}
                      </a>
                    </td>
                    <td className="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{img.pushedAt}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>
    </Layout>
  );
}

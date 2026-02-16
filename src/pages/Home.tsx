import { useMemo } from 'react';
import { Link } from 'react-router';
import { BarChart3, FolderOpen, Box, CheckCircle, XCircle, AlertCircle, GitPullRequest, ArrowUp, ArrowDown, ArrowRight } from 'lucide-react';
import Layout from '../components/Layout';
import TrendChart from '../components/TrendChart';
import { useWorkflowStatuses, useImageBuilds, useHistory, useIssuesPRs } from '../hooks/useData';
import { computeTrend } from '../utils/chartStyles';
import type { Trend } from '../utils/chartStyles';
import { projects } from '../data/projects';

const HOURS_48 = 48 * 60 * 60 * 1000;

export default function Home() {
  const { data: workflows } = useWorkflowStatuses();
  const { data: images } = useImageBuilds();
  const { data: history } = useHistory();
  const { data: issuesPRs } = useIssuesPRs();

  const chartData = useMemo(() => {
    if (!history) return [];
    const cutoff = Date.now() - 7 * 24 * 60 * 60 * 1000;
    return history.snapshots
      .filter((s) => new Date(s.timestamp).getTime() >= cutoff)
      .map((s) => ({
        date: s.timestamp,
        success: s.workflows['success'] ?? 0,
        failure: s.workflows['failure'] ?? 0,
      }));
  }, [history]);

  const cutoff = Date.now() - HOURS_48;
  const recent = workflows.filter((w) => new Date(w.updatedAt).getTime() >= cutoff);
  const totalPassed = recent.filter((w) => w.status === 'success').length;
  const totalFailed = recent.filter((w) => w.status === 'failure').length;

  const totalOpenIssues = useMemo(() => {
    if (!issuesPRs) return 0;
    return Object.values(issuesPRs.repos).reduce((sum, r) => sum + r.issues.total, 0);
  }, [issuesPRs]);

  const totalOpenPRs = useMemo(() => {
    if (!issuesPRs) return 0;
    return Object.values(issuesPRs.repos).reduce((sum, r) => sum + r.pullRequests.total, 0);
  }, [issuesPRs]);

  const issuesPRsRows = useMemo(() => {
    if (!issuesPRs) return [];
    return projects
      .map((p) => {
        const repoData = issuesPRs.repos[p.repo.toLowerCase()];
        if (!repoData) return null;

        const buckets = repoData.issues.ageBuckets;
        let oldestBucket = '';
        if (buckets.ancient > 0) oldestBucket = '>1yr';
        else if (buckets.stale > 0) oldestBucket = '90d-1yr';
        else if (buckets.aging > 0) oldestBucket = '30-90d';
        else if (buckets.recent > 0) oldestBucket = '7-30d';
        else if (buckets.fresh > 0) oldestBucket = '<7d';
        else oldestBucket = 'none';

        const trend = computeTrend(repoData.issues.velocity);

        return {
          project: p,
          openIssues: repoData.issues.total,
          openPRs: repoData.pullRequests.total,
          oldestBucket,
          awaitingReview: repoData.pullRequests.review.awaitingReview,
          avgMergeDays: repoData.pullRequests.review.avgDaysToMerge,
          trend,
        };
      })
      .filter(Boolean) as Array<{
        project: typeof projects[0];
        openIssues: number;
        openPRs: number;
        oldestBucket: string;
        awaitingReview: number;
        avgMergeDays: number;
        trend: Trend;
      }>;
  }, [issuesPRs]);

  const stats = [
    { label: 'Projects', value: projects.length, icon: FolderOpen, color: 'text-nvidia-green', to: '/projects' },
    { label: 'Workflows Passing (48h)', value: totalPassed, icon: CheckCircle, color: 'text-status-pass', to: '/dashboard#workflow-status' },
    { label: 'Workflows Failing (48h)', value: totalFailed, icon: XCircle, color: totalFailed > 0 ? 'text-status-fail' : 'text-status-pass', to: '/dashboard#workflow-status' },
    { label: 'Image Builds', value: images.length, icon: Box, color: 'text-blue-500', to: '/dashboard#image-builds' },
    { label: 'Open Issues', value: totalOpenIssues, icon: AlertCircle, color: 'text-orange-500', to: '/dashboard#issues-prs' },
    { label: 'Open PRs', value: totalOpenPRs, icon: GitPullRequest, color: 'text-purple-500', to: '/dashboard#issues-prs' },
  ];

  return (
    <Layout>
      <div className="mb-8">
        <h1 className="text-3xl font-bold text-gray-900 dark:text-white mb-2">Cloud Native Test Infrastructure</h1>
        <p className="text-lg text-gray-600 dark:text-gray-400">Dashboard and portfolio for NVIDIA's cloud-native Kubernetes projects.</p>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-3 gap-4 mb-8">
        {stats.map(({ label, value, icon: Icon, color, to }) => (
          <Link key={label} to={to} className="bg-white dark:bg-gray-800 rounded-lg shadow p-5 flex items-center gap-4 hover:shadow-md transition-shadow cursor-pointer">
            <Icon size={32} className={color} />
            <div>
              <p className="text-2xl font-bold text-gray-900 dark:text-white">{value}</p>
              <p className="text-sm text-gray-500 dark:text-gray-400">{label}</p>
            </div>
          </Link>
        ))}
      </div>

      {chartData.length > 0 && (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-6 mb-8">
          <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">
            Workflow Trends (7 days)
          </h2>
          <TrendChart
            data={chartData}
            areas={[
              { key: 'success', color: '#22c55e', name: 'Success' },
              { key: 'failure', color: '#ef4444', name: 'Failure' },
            ]}
            height={180}
            stacked
          />
        </div>
      )}

      {issuesPRsRows.length > 0 && (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow mb-8">
          <div className="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
            <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200">Issue &amp; PR Health</h2>
          </div>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Project</th>
                  <th className="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Open Issues</th>
                  <th className="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Open PRs</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Oldest Issue</th>
                  <th className="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Awaiting Review</th>
                  <th className="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Avg Merge (d)</th>
                  <th className="px-4 py-3 text-center text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Trend</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {issuesPRsRows.map((row) => (
                  <tr key={row.project.slug}>
                    <td className="px-4 py-3 text-sm">
                      <Link to={`/projects/${row.project.slug}#issues-prs`} className="text-nvidia-green hover:text-nvidia-green-dark font-medium">
                        {row.project.name}
                      </Link>
                    </td>
                    <td className="px-4 py-3 text-sm text-right text-gray-900 dark:text-white">{row.openIssues}</td>
                    <td className="px-4 py-3 text-sm text-right text-gray-900 dark:text-white">{row.openPRs}</td>
                    <td className="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{row.oldestBucket}</td>
                    <td className="px-4 py-3 text-sm text-right text-gray-900 dark:text-white">{row.awaitingReview}</td>
                    <td className="px-4 py-3 text-sm text-right text-gray-900 dark:text-white">{row.avgMergeDays.toFixed(1)}</td>
                    <td className="px-4 py-3 text-center">
                      {row.trend === 'growing' && <ArrowUp size={16} className="inline text-red-500" />}
                      {row.trend === 'shrinking' && <ArrowDown size={16} className="inline text-green-500" />}
                      {row.trend === 'stable' && <ArrowRight size={16} className="inline text-gray-400" />}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <Link to="/dashboard" className="bg-white dark:bg-gray-800 rounded-lg shadow hover:shadow-md transition-shadow border border-gray-200 dark:border-gray-700 p-6">
          <div className="flex items-center gap-3 mb-2">
            <BarChart3 size={24} className="text-nvidia-green" />
            <h2 className="text-xl font-semibold text-gray-900 dark:text-white">Dashboard</h2>
          </div>
          <p className="text-gray-600 dark:text-gray-400">View E2E test results, workflow statuses, and latest image builds across all projects.</p>
        </Link>
        <Link to="/projects" className="bg-white dark:bg-gray-800 rounded-lg shadow hover:shadow-md transition-shadow border border-gray-200 dark:border-gray-700 p-6">
          <div className="flex items-center gap-3 mb-2">
            <FolderOpen size={24} className="text-nvidia-green" />
            <h2 className="text-xl font-semibold text-gray-900 dark:text-white">Projects</h2>
          </div>
          <p className="text-gray-600 dark:text-gray-400">Browse all {projects.length} NVIDIA cloud-native Kubernetes projects with documentation and CI status.</p>
        </Link>
      </div>
    </Layout>
  );
}

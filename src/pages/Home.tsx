import { useMemo } from 'react';
import { Link } from 'react-router';
import { BarChart3, FolderOpen, Box, CheckCircle, XCircle } from 'lucide-react';
import Layout from '../components/Layout';
import TrendChart from '../components/TrendChart';
import { useWorkflowStatuses, useImageBuilds, useHistory } from '../hooks/useData';
import { projects } from '../data/projects';

const HOURS_48 = 48 * 60 * 60 * 1000;

export default function Home() {
  const { data: workflows } = useWorkflowStatuses();
  const { data: images } = useImageBuilds();
  const { data: history } = useHistory();

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

  const stats = [
    { label: 'Projects', value: projects.length, icon: FolderOpen, color: 'text-nvidia-green' },
    { label: 'Workflows Passing (48h)', value: totalPassed, icon: CheckCircle, color: 'text-status-pass' },
    { label: 'Workflows Failing (48h)', value: totalFailed, icon: XCircle, color: totalFailed > 0 ? 'text-status-fail' : 'text-status-pass' },
    { label: 'Image Builds', value: images.length, icon: Box, color: 'text-blue-500' },
  ];

  return (
    <Layout>
      <div className="mb-8">
        <h1 className="text-3xl font-bold text-gray-900 dark:text-white mb-2">Cloud Native Test Infrastructure</h1>
        <p className="text-lg text-gray-600 dark:text-gray-400">Dashboard and portfolio for NVIDIA's cloud-native Kubernetes projects.</p>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
        {stats.map(({ label, value, icon: Icon, color }) => (
          <div key={label} className="bg-white dark:bg-gray-800 rounded-lg shadow p-5 flex items-center gap-4">
            <Icon size={32} className={color} />
            <div>
              <p className="text-2xl font-bold text-gray-900 dark:text-white">{value}</p>
              <p className="text-sm text-gray-500 dark:text-gray-400">{label}</p>
            </div>
          </div>
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

import { ExternalLink } from 'lucide-react';
import Layout from '../components/Layout';
import StatusBadge from '../components/StatusBadge';
import { useTestResults, useWorkflowStatuses, useImageBuilds } from '../hooks/useData';

const sidebarItems = [
  { to: '/dashboard', label: 'E2E Test Results' },
  { to: '/dashboard/images', label: 'Image Builds' },
];

export default function Dashboard() {
  const { data: results, loading: resultsLoading } = useTestResults();
  const { data: workflows, loading: workflowsLoading } = useWorkflowStatuses();
  const { data: images, loading: imagesLoading } = useImageBuilds();

  return (
    <Layout sidebarItems={sidebarItems} sidebarTitle="Dashboard">
      <h1 className="text-2xl font-bold text-gray-900 mb-6">Dashboard</h1>

      {/* E2E Test Results */}
      <section className="mb-8">
        <h2 className="text-lg font-semibold text-gray-800 mb-3">E2E Test Results</h2>
        <div className="bg-white rounded-lg shadow overflow-x-auto">
          {resultsLoading ? (
            <p className="p-4 text-gray-500">Loading...</p>
          ) : (
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Project</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Last Run</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Passed</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Failed</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Source</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Run</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200">
                {results.map((r) => (
                  <tr key={`${r.repo}-${r.project}`}>
                    <td className="px-4 py-3 text-sm text-gray-900">{r.project}</td>
                    <td className="px-4 py-3 text-sm text-gray-500">{r.lastRun}</td>
                    <td className="px-4 py-3 text-sm text-status-pass font-medium">{r.passed}</td>
                    <td className="px-4 py-3 text-sm text-status-fail font-medium">{r.failed}</td>
                    <td className="px-4 py-3">
                      <span className="inline-block rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-600">
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
      <section className="mb-8">
        <h2 className="text-lg font-semibold text-gray-800 mb-3">Workflow Status</h2>
        <div className="bg-white rounded-lg shadow overflow-x-auto">
          {workflowsLoading ? (
            <p className="p-4 text-gray-500">Loading...</p>
          ) : (
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Repo</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Workflow</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Status</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Updated</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Run</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200">
                {workflows.map((w) => (
                  <tr key={`${w.repo}-${w.workflow}`}>
                    <td className="px-4 py-3 text-sm text-gray-900">{w.repo}</td>
                    <td className="px-4 py-3 text-sm text-gray-700">{w.workflow}</td>
                    <td className="px-4 py-3">
                      <StatusBadge status={w.status} />
                    </td>
                    <td className="px-4 py-3 text-sm text-gray-500">{w.updatedAt}</td>
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

      {/* Latest Image Builds */}
      <section className="mb-8">
        <h2 className="text-lg font-semibold text-gray-800 mb-3">Latest Image Builds</h2>
        <div className="bg-white rounded-lg shadow overflow-x-auto">
          {imagesLoading ? (
            <p className="p-4 text-gray-500">Loading...</p>
          ) : (
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Repo</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Tag</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Pushed At</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200">
                {images.map((img) => (
                  <tr key={`${img.repo}-${img.tag}`}>
                    <td className="px-4 py-3 text-sm text-gray-900">{img.repo}</td>
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
                    <td className="px-4 py-3 text-sm text-gray-500">{img.pushedAt}</td>
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

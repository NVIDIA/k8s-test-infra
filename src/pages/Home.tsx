import { Link } from 'react-router';
import { BarChart3, FolderOpen, Box, CheckCircle } from 'lucide-react';
import Layout from '../components/Layout';
import { useTestResults, useImageBuilds } from '../hooks/useData';
import { projects } from '../data/projects';

export default function Home() {
  const { data: results } = useTestResults();
  const { data: images } = useImageBuilds();

  const totalPassed = results.reduce((sum, r) => sum + r.passed, 0);
  const totalFailed = results.reduce((sum, r) => sum + r.failed, 0);

  const stats = [
    { label: 'Projects', value: projects.length, icon: FolderOpen, color: 'text-nvidia-green' },
    { label: 'Tests Passing', value: totalPassed, icon: CheckCircle, color: 'text-status-pass' },
    { label: 'Tests Failing', value: totalFailed, icon: BarChart3, color: totalFailed > 0 ? 'text-status-fail' : 'text-status-pass' },
    { label: 'Image Builds', value: images.length, icon: Box, color: 'text-blue-500' },
  ];

  return (
    <Layout>
      <div className="mb-8">
        <h1 className="text-3xl font-bold text-gray-900 mb-2">Cloud Native Test Infrastructure</h1>
        <p className="text-lg text-gray-600">Dashboard and portfolio for NVIDIA's cloud-native Kubernetes projects.</p>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
        {stats.map(({ label, value, icon: Icon, color }) => (
          <div key={label} className="bg-white rounded-lg shadow p-5 flex items-center gap-4">
            <Icon size={32} className={color} />
            <div>
              <p className="text-2xl font-bold text-gray-900">{value}</p>
              <p className="text-sm text-gray-500">{label}</p>
            </div>
          </div>
        ))}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <Link to="/dashboard" className="bg-white rounded-lg shadow hover:shadow-md transition-shadow border border-gray-200 p-6">
          <div className="flex items-center gap-3 mb-2">
            <BarChart3 size={24} className="text-nvidia-green" />
            <h2 className="text-xl font-semibold text-gray-900">Dashboard</h2>
          </div>
          <p className="text-gray-600">View E2E test results, workflow statuses, and latest image builds across all projects.</p>
        </Link>
        <Link to="/projects" className="bg-white rounded-lg shadow hover:shadow-md transition-shadow border border-gray-200 p-6">
          <div className="flex items-center gap-3 mb-2">
            <FolderOpen size={24} className="text-nvidia-green" />
            <h2 className="text-xl font-semibold text-gray-900">Projects</h2>
          </div>
          <p className="text-gray-600">Browse all {projects.length} NVIDIA cloud-native Kubernetes projects with documentation and CI status.</p>
        </Link>
      </div>
    </Layout>
  );
}

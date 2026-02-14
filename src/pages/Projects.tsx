import { Link } from 'react-router';
import { ExternalLink, Box, Cpu, HardDrive, TestTube, Library } from 'lucide-react';
import Layout from '../components/Layout';
import { projects } from '../data/projects';
import type { Project } from '../types';

const categoryIcons: Record<Project['category'], typeof Box> = {
  operator: Cpu,
  runtime: Box,
  driver: HardDrive,
  testing: TestTube,
  library: Library,
};

const categoryColors: Record<Project['category'], string> = {
  operator: 'bg-blue-100 text-blue-800',
  runtime: 'bg-purple-100 text-purple-800',
  driver: 'bg-orange-100 text-orange-800',
  testing: 'bg-green-100 text-green-800',
  library: 'bg-gray-100 text-gray-800',
};

export default function Projects() {
  return (
    <Layout>
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900 mb-2">Projects</h1>
        <p className="text-gray-600">
          NVIDIA cloud-native Kubernetes projects for GPU workloads.
        </p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {projects.map((project) => {
          const Icon = categoryIcons[project.category];
          return (
            <div
              key={project.slug}
              className="bg-white rounded-lg shadow hover:shadow-md transition-shadow border border-gray-200 p-6 flex flex-col"
            >
              <div className="flex items-start justify-between mb-3">
                <div className="flex items-center gap-2">
                  <Icon size={20} className="text-nvidia-green" />
                  <h2 className="text-lg font-semibold text-gray-900">{project.name}</h2>
                </div>
                <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ${categoryColors[project.category]}`}>
                  {project.category}
                </span>
              </div>
              <p className="text-sm text-gray-600 mb-4 flex-1">{project.description}</p>
              <div className="flex items-center gap-3 pt-3 border-t border-gray-100">
                <a
                  href={`https://github.com/${project.repo}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-sm text-nvidia-green hover:text-nvidia-green-dark"
                >
                  <ExternalLink size={14} />
                  GitHub
                </a>
                <Link
                  to={`/projects/${project.slug}`}
                  className="inline-flex items-center gap-1 text-sm text-gray-500 hover:text-gray-700"
                >
                  Details →
                </Link>
              </div>
            </div>
          );
        })}
      </div>
    </Layout>
  );
}

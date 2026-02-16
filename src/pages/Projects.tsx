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
  operator: 'bg-blue-100 dark:bg-blue-900/40 text-blue-800 dark:text-blue-300',
  runtime: 'bg-purple-100 dark:bg-purple-900/40 text-purple-800 dark:text-purple-300',
  driver: 'bg-orange-100 dark:bg-orange-900/40 text-orange-800 dark:text-orange-300',
  testing: 'bg-green-100 dark:bg-green-900/40 text-green-800 dark:text-green-300',
  library: 'bg-gray-100 dark:bg-gray-700 text-gray-800 dark:text-gray-200',
};

export default function Projects() {
  return (
    <Layout>
      <div className="max-w-7xl mx-auto w-full">
        <div className="mb-8">
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white mb-2">Projects</h1>
          <p className="text-gray-600 dark:text-gray-400">
            NVIDIA cloud-native Kubernetes projects for GPU workloads.
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          {projects.map((project) => {
            const Icon = categoryIcons[project.category];
            return (
              <Link
                key={project.slug}
                to={`/projects/${project.slug}`}
                className="bg-white dark:bg-gray-800 rounded-lg shadow hover:shadow-md transition-shadow border border-gray-200 dark:border-gray-700 p-6 flex flex-col"
              >
                <div className="flex items-start justify-between mb-3">
                  <div className="flex items-center gap-2">
                    <Icon size={20} className="text-nvidia-green" />
                    <h2 className="text-lg font-semibold text-gray-900 dark:text-white">{project.name}</h2>
                  </div>
                  <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ${categoryColors[project.category]}`}>
                    {project.category}
                  </span>
                </div>
                <p className="text-sm text-gray-600 dark:text-gray-400 mb-4 flex-1">{project.description}</p>
                <div className="flex items-center gap-3 pt-3 border-t border-gray-100 dark:border-gray-700">
                  <span
                    onClick={(e) => {
                      e.preventDefault();
                      window.open(`https://github.com/${project.repo}`, '_blank');
                    }}
                    className="inline-flex items-center gap-1 text-sm text-nvidia-green hover:text-nvidia-green-dark"
                  >
                    <ExternalLink size={14} />
                    GitHub
                  </span>
                </div>
              </Link>
            );
          })}
        </div>
      </div>
    </Layout>
  );
}

import { useParams, Link } from 'react-router';
import { ExternalLink, Star, Code, Scale, ArrowLeft } from 'lucide-react';
import Layout from '../components/Layout';
import StatusBadge from '../components/StatusBadge';
import { useRepoInfos, useWorkflowStatuses, useImageBuilds } from '../hooks/useData';
import { projects } from '../data/projects';

export default function ProjectDetail() {
  const { slug } = useParams<{ slug: string }>();
  const project = projects.find((p) => p.slug === slug);

  const { data: repos, loading: reposLoading } = useRepoInfos();
  const { data: workflows } = useWorkflowStatuses();
  const { data: images } = useImageBuilds();

  if (!project) {
    return (
      <Layout>
        <div className="text-center py-12">
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white mb-4">Project Not Found</h1>
          <Link to="/projects" className="text-nvidia-green hover:text-nvidia-green-dark">
            &larr; Back to Projects
          </Link>
        </div>
      </Layout>
    );
  }

  const repoKey = project.repo.toLowerCase();
  const repoInfo = repos.find((r) => r.fullName.toLowerCase() === repoKey);
  const projectWorkflows = workflows.filter((w) => w.repo.toLowerCase() === repoKey);
  const projectImages = images.filter((i) => i.repo.toLowerCase() === repoKey);

  const sidebarItems = [
    { to: `/projects/${slug}`, label: 'Overview' },
    { to: `/projects/${slug}#ci-status`, label: 'CI Status' },
    ...(projectImages.length > 0 ? [{ to: `/projects/${slug}#images`, label: 'Images' }] : []),
    { to: `/projects/${slug}#readme`, label: 'README' },
  ];

  return (
    <Layout sidebarItems={sidebarItems} sidebarTitle={project.name}>
      {/* Back link */}
      <Link
        to="/projects"
        className="inline-flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400 hover:text-nvidia-green mb-4"
      >
        <ArrowLeft size={14} />
        All Projects
      </Link>

      {/* Header */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-6 mb-6">
        <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold text-gray-900 dark:text-white mb-1">{project.name}</h1>
            <p className="text-gray-600 dark:text-gray-400 mb-3">
              {repoInfo?.description || project.description}
            </p>
            {repoInfo?.topics && repoInfo.topics.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mb-3">
                {repoInfo.topics.map((t) => (
                  <span
                    key={t}
                    className="inline-block bg-nvidia-green/10 text-nvidia-green-dark rounded-full px-2.5 py-0.5 text-xs font-medium"
                  >
                    {t}
                  </span>
                ))}
              </div>
            )}
          </div>
          <a
            href={`https://github.com/${project.repo}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 shrink-0 bg-nvidia-black text-white px-4 py-2 rounded-md text-sm hover:bg-gray-700 transition-colors"
          >
            <ExternalLink size={14} />
            View on GitHub
          </a>
        </div>

        {/* Metadata badges */}
        {repoInfo && (
          <div className="flex flex-wrap gap-4 pt-3 border-t border-gray-100 dark:border-gray-700 text-sm text-gray-600 dark:text-gray-400">
            <span className="inline-flex items-center gap-1">
              <Star size={14} className="text-yellow-500" />
              {repoInfo.stars.toLocaleString()} stars
            </span>
            {repoInfo.language && (
              <span className="inline-flex items-center gap-1">
                <Code size={14} />
                {repoInfo.language}
              </span>
            )}
            {repoInfo.license && (
              <span className="inline-flex items-center gap-1">
                <Scale size={14} />
                {repoInfo.license}
              </span>
            )}
          </div>
        )}
        {reposLoading && (
          <p className="text-sm text-gray-400 dark:text-gray-500 mt-2">Loading repo info...</p>
        )}
      </div>

      {/* CI Status */}
      <section id="ci-status" className="mb-6">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">CI Status</h2>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
          {projectWorkflows.length === 0 ? (
            <p className="p-4 text-gray-500 dark:text-gray-400">No workflow data available.</p>
          ) : (
            <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Workflow</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Status</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Updated</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Run</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {projectWorkflows.map((w) => (
                  <tr key={w.workflow}>
                    <td className="px-4 py-3 text-sm text-gray-900 dark:text-white">{w.workflow}</td>
                    <td className="px-4 py-3">
                      <StatusBadge status={w.status} />
                    </td>
                    <td className="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{w.updatedAt}</td>
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

      {/* Images */}
      {projectImages.length > 0 && (
        <section id="images" className="mb-6">
          <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">Container Images</h2>
          <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
            <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Tag</th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Pushed At</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {projectImages.map((img) => (
                  <tr key={img.tag}>
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
          </div>
        </section>
      )}

      {/* README */}
      <section id="readme" className="mb-6">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">README</h2>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-6 markdown-body">
          {reposLoading ? (
            <p className="text-gray-400 dark:text-gray-500">Loading README...</p>
          ) : repoInfo?.readme ? (
            <div dangerouslySetInnerHTML={{ __html: repoInfo.readme }} />
          ) : (
            <p className="text-gray-500 dark:text-gray-400">No README available.</p>
          )}
        </div>
      </section>
    </Layout>
  );
}

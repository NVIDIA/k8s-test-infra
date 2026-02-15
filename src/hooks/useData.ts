import { useState, useEffect } from 'react';
import type { TestResult, WorkflowStatus, ImageBuild, RepoInfo, HistoryFile } from '../types';

const BASE = import.meta.env.BASE_URL;

interface ResultsData {
  results: TestResult[];
}

interface WorkflowsData {
  workflows: WorkflowStatus[];
}

interface ImagesData {
  images: ImageBuild[];
}

interface ReposData {
  repos: RepoInfo[];
}

async function fetchJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}data/${path}`);
  if (!res.ok) throw new Error(`Failed to fetch ${path}: ${res.status}`);
  return res.json() as Promise<T>;
}

export function useTestResults() {
  const [data, setData] = useState<TestResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchJSON<ResultsData>('results.json')
      .then((d) => setData(d.results ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  return { data, loading, error };
}

export function useWorkflowStatuses() {
  const [data, setData] = useState<WorkflowStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchJSON<WorkflowsData>('workflows.json')
      .then((d) => setData(d.workflows ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  return { data, loading, error };
}

export function useImageBuilds() {
  const [data, setData] = useState<ImageBuild[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchJSON<ImagesData>('images.json')
      .then((d) => setData(d.images ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  return { data, loading, error };
}

export function useRepoInfos() {
  const [data, setData] = useState<RepoInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchJSON<ReposData>('repos.json')
      .then((d) => setData(d.repos ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  return { data, loading, error };
}

export function useHistory() {
  const [data, setData] = useState<HistoryFile | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchJSON<HistoryFile>('history.json')
      .then((d) => {
        setData({
          snapshots: d.snapshots ?? [],
          traffic: d.traffic ?? {},
          repoStats: d.repoStats ?? {},
        });
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  return { data, loading, error };
}

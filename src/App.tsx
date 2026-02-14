import { Routes, Route } from 'react-router';
import Layout from './components/Layout';

function HomePage() {
  return (
    <Layout>
      <h1 className="text-2xl font-bold text-gray-900 mb-4">
        Cloud Native Test Infrastructure
      </h1>
      <p className="text-gray-600">Dashboard and project portfolio coming soon.</p>
    </Layout>
  );
}

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<HomePage />} />
    </Routes>
  );
}

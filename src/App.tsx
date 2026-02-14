import { Routes, Route } from 'react-router';

export default function App() {
  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-nvidia-black text-white p-4">
        <h1 className="text-xl font-bold">Cloud Native Test Infrastructure</h1>
      </header>
      <main className="p-8">
        <Routes>
          <Route path="/" element={<p className="text-gray-700">Dashboard coming soon.</p>} />
        </Routes>
      </main>
    </div>
  );
}

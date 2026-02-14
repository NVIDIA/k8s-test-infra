import { Routes, Route, Navigate } from 'react-router';
import Dashboard from './pages/Dashboard';
import Projects from './pages/Projects';

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/dashboard" replace />} />
      <Route path="/dashboard" element={<Dashboard />} />
      <Route path="/projects" element={<Projects />} />
    </Routes>
  );
}

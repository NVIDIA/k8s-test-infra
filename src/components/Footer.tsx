export default function Footer() {
  return (
    <footer className="bg-nvidia-black text-gray-400 py-6 mt-auto">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="flex flex-col sm:flex-row items-center justify-between gap-2 text-sm">
          <span>&copy; 2026 NVIDIA Corporation. All Rights Reserved.</span>
          <div className="flex gap-4">
            <a
              href="https://github.com/nvidia/k8s-test-infra"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-white transition-colors"
            >
              GitHub
            </a>
            <a
              href="https://www.nvidia.com/en-us/about-nvidia/privacy-policy/"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-white transition-colors"
            >
              Privacy Policy
            </a>
          </div>
        </div>
      </div>
    </footer>
  );
}

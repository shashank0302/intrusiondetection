import LiveMonitor from '@/components/LiveMonitor'

export default function Home() {
  return (
    <main className="min-h-screen p-6">
      <div className="max-w-7xl mx-auto">
        {/* Header */}
        <header className="mb-8">
          <div className="flex items-center gap-4">
            <div className="w-3 h-3 bg-emerald-500 rounded-full animate-pulse"></div>
            <h1 className="text-3xl font-bold tracking-tight">
              <span className="text-cyan-400">IDS</span> Dashboard
            </h1>
          </div>
          <p className="text-gray-500 mt-2 text-sm">
            Real-time Intrusion Detection System Monitor
          </p>
        </header>

        {/* Live Monitor Component */}
        <LiveMonitor />
      </div>
    </main>
  )
}

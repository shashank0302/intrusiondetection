'use client'

import { useEffect, useState, useRef } from 'react'
import {
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
  Area,
  AreaChart,
} from 'recharts'

interface DataPoint {
  time: string
  timestamp: number
  rps: number
  blocked: number
}

interface Alert {
  id: number
  timestamp: string
  blocked: number
  rps: number
}

interface AIAlert {
  id: number
  timestamp: string
  ip: string
  payloadSize: number
}

const WS_URL = 'ws://localhost:8080/ws'
const MAX_DATA_POINTS = 60

export default function LiveMonitor() {
  const [data, setData] = useState<DataPoint[]>([])
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [aiAlerts, setAiAlerts] = useState<AIAlert[]>([])
  const [connected, setConnected] = useState(false)
  const [currentRPS, setCurrentRPS] = useState(0)
  const [currentBlocked, setCurrentBlocked] = useState(0)
  const [totalRequests, setTotalRequests] = useState(0)
  const [totalBlocked, setTotalBlocked] = useState(0)
  const [totalAIAlerts, setTotalAIAlerts] = useState(0)
  const wsRef = useRef<WebSocket | null>(null)
  const alertIdRef = useRef(0)
  const aiAlertIdRef = useRef(0)

  useEffect(() => {
    const connect = () => {
      const ws = new WebSocket(WS_URL)
      wsRef.current = ws

      ws.onopen = () => {
        console.log('WebSocket connected')
        setConnected(true)
      }

      ws.onclose = () => {
        console.log('WebSocket disconnected')
        setConnected(false)
        setTimeout(connect, 2000)
      }

      ws.onerror = (error) => {
        console.error('WebSocket error:', error)
      }

      ws.onmessage = (event) => {
        try {
          const payload = JSON.parse(event.data)
          const now = new Date()
          const timeStr = now.toLocaleTimeString('en-US', {
            hour12: false,
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
          })

          // Check if this is an AI alert
          if (payload.type === 'ai_alert') {
            const newAIAlert: AIAlert = {
              id: aiAlertIdRef.current++,
              timestamp: timeStr,
              ip: payload.ip,
              payloadSize: payload.payload_size,
            }
            setAiAlerts((prev) => [newAIAlert, ...prev].slice(0, 50))
            setTotalAIAlerts((prev) => prev + 1)
            return
          }

          // Regular stats payload
          const newPoint: DataPoint = {
            time: timeStr,
            timestamp: payload.timestamp,
            rps: payload.rps,
            blocked: payload.blocked,
          }

          setCurrentRPS(payload.rps)
          setCurrentBlocked(payload.blocked)
          setTotalRequests((prev) => prev + payload.rps)
          setTotalBlocked((prev) => prev + payload.blocked)

          setData((prev) => {
            const updated = [...prev, newPoint]
            return updated.slice(-MAX_DATA_POINTS)
          })

          if (payload.blocked > 0) {
            const newAlert: Alert = {
              id: alertIdRef.current++,
              timestamp: timeStr,
              blocked: payload.blocked,
              rps: payload.rps,
            }
            setAlerts((prev) => [newAlert, ...prev].slice(0, 20))
          }
        } catch (e) {
          console.error('Parse error:', e)
        }
      }
    }

    connect()

    return () => {
      wsRef.current?.close()
    }
  }, [])

  const blockRate = currentRPS > 0 ? ((currentBlocked / currentRPS) * 100).toFixed(1) : '0.0'

  return (
    <div className="space-y-6">
      {/* Connection Status */}
      <div className="flex items-center gap-2 text-sm">
        <div
          className={`w-2 h-2 rounded-full ${
            connected ? 'bg-emerald-500' : 'bg-red-500 animate-pulse'
          }`}
        ></div>
        <span className="text-gray-400">
          {connected ? 'Connected to WebSocket' : 'Connecting...'}
        </span>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
        <StatCard
          title="Requests/sec"
          value={currentRPS.toLocaleString()}
          color="cyan"
          subtitle="Current RPS"
        />
        <StatCard
          title="Blocked/sec"
          value={currentBlocked.toLocaleString()}
          color="red"
          subtitle="Attacks blocked"
        />
        <StatCard
          title="Block Rate"
          value={`${blockRate}%`}
          color="amber"
          subtitle="% of traffic blocked"
        />
        <StatCard
          title="Total Blocked"
          value={totalBlocked.toLocaleString()}
          color="purple"
          subtitle="Since page load"
        />
        <StatCard
          title="AI Detections"
          value={totalAIAlerts.toLocaleString()}
          color="emerald"
          subtitle="Zero-day alerts"
        />
      </div>

      {/* Main Chart */}
      <div className="bg-gray-900/50 rounded-xl p-6 border border-gray-800">
        <h2 className="text-lg font-semibold mb-4 text-gray-200">
          Traffic Monitor <span className="text-gray-500 text-sm">(last 60 seconds)</span>
        </h2>
        <div className="h-80">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={data} margin={{ top: 10, right: 30, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="colorRps" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#06b6d4" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="#06b6d4" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="colorBlocked" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#ef4444" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="#ef4444" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" stroke="#374151" />
              <XAxis
                dataKey="time"
                stroke="#6b7280"
                tick={{ fill: '#9ca3af', fontSize: 11 }}
                interval="preserveStartEnd"
              />
              <YAxis stroke="#6b7280" tick={{ fill: '#9ca3af', fontSize: 11 }} />
              <Tooltip
                contentStyle={{
                  backgroundColor: '#1f2937',
                  border: '1px solid #374151',
                  borderRadius: '8px',
                }}
                labelStyle={{ color: '#f3f4f6' }}
              />
              <Legend />
              <Area
                type="monotone"
                dataKey="rps"
                name="Traffic (RPS)"
                stroke="#06b6d4"
                strokeWidth={2}
                fillOpacity={1}
                fill="url(#colorRps)"
              />
              <Area
                type="monotone"
                dataKey="blocked"
                name="Attacks (Blocked)"
                stroke="#ef4444"
                strokeWidth={2}
                fillOpacity={1}
                fill="url(#colorBlocked)"
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Alerts Grid */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Rate Limit Alerts */}
        <div className="bg-gray-900/50 rounded-xl p-6 border border-gray-800">
          <h2 className="text-lg font-semibold mb-4 text-gray-200 flex items-center gap-2">
            <span className="w-2 h-2 bg-red-500 rounded-full pulse-alert"></span>
            Rate Limit Blocks
          </h2>
          <div className="h-64 overflow-y-auto space-y-2">
            {alerts.length === 0 ? (
              <p className="text-gray-500 text-sm">No rate limit blocks yet...</p>
            ) : (
              alerts.map((alert) => (
                <div
                  key={alert.id}
                  className="flex items-center justify-between bg-red-950/30 border border-red-900/50 rounded-lg px-4 py-2 text-sm"
                >
                  <div className="flex items-center gap-3">
                    <span className="text-red-400">⚠</span>
                    <span className="text-gray-400">{alert.timestamp}</span>
                    <span className="text-red-400 font-medium">
                      {alert.blocked} blocked
                    </span>
                  </div>
                  <span className="text-gray-500">{alert.rps} req/s</span>
                </div>
              ))
            )}
          </div>
        </div>

        {/* AI Zero-Day Alerts */}
        <div className="bg-gray-900/50 rounded-xl p-6 border border-emerald-800/50">
          <h2 className="text-lg font-semibold mb-4 text-gray-200 flex items-center gap-2">
            <span className="w-2 h-2 bg-emerald-500 rounded-full pulse-alert"></span>
            🤖 AI Zero-Day Detections
          </h2>
          <div className="h-64 overflow-y-auto space-y-2">
            {aiAlerts.length === 0 ? (
              <p className="text-gray-500 text-sm">AI worker analyzing traffic...</p>
            ) : (
              aiAlerts.map((alert) => (
                <div
                  key={alert.id}
                  className="flex items-center justify-between bg-emerald-950/30 border border-emerald-900/50 rounded-lg px-4 py-2 text-sm"
                >
                  <div className="flex items-center gap-3">
                    <span className="text-emerald-400">🧠</span>
                    <span className="text-gray-400">{alert.timestamp}</span>
                    <span className="text-emerald-400 font-medium font-mono">
                      {alert.ip}
                    </span>
                  </div>
                  <span className="text-gray-500">
                    {alert.payloadSize} bytes
                  </span>
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

function StatCard({
  title,
  value,
  color,
  subtitle,
}: {
  title: string
  value: string
  color: 'cyan' | 'red' | 'amber' | 'purple' | 'emerald'
  subtitle: string
}) {
  const colorClasses = {
    cyan: 'border-cyan-500/30 text-cyan-400',
    red: 'border-red-500/30 text-red-400',
    amber: 'border-amber-500/30 text-amber-400',
    purple: 'border-purple-500/30 text-purple-400',
    emerald: 'border-emerald-500/30 text-emerald-400',
  }

  return (
    <div className={`bg-gray-900/50 rounded-xl p-4 border ${colorClasses[color]}`}>
      <p className="text-gray-500 text-xs uppercase tracking-wider">{title}</p>
      <p className={`text-2xl font-bold mt-1 ${colorClasses[color].split(' ')[1]}`}>
        {value}
      </p>
      <p className="text-gray-600 text-xs mt-1">{subtitle}</p>
    </div>
  )
}

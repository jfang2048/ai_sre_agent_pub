import { useState, useEffect } from 'react'
import {
  Activity,
  Cpu,
  HardDrive,
  Network,
  AlertTriangle,
  CheckCircle,
  XCircle,
  Clock,
  TrendingUp,
  TrendingDown
} from 'lucide-react'
import {
  LineChart, Line, AreaChart, Area, BarChart, Bar, PieChart, Pie, Cell,
  XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer
} from 'recharts'
import './Dashboard.css'

const API_BASE = '/api/v1'

function Dashboard() {
  const [status, setStatus] = useState(null)
  const [metrics, setMetrics] = useState([])
  const [sloStatus, setSloStatus] = useState(null)
  const [dashboardData, setDashboardData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [cpuHistory, setCpuHistory] = useState([])
  const [memHistory, setMemHistory] = useState([])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 2000)
    return () => clearInterval(interval)
  }, [])

  const fetchData = async () => {
    try {
      const [statusRes, metricsRes, sloRes, dashRes, historyRes] = await Promise.all([
        fetch(`${API_BASE}/status`),
        fetch(`${API_BASE}/metrics`),
        fetch(`${API_BASE}/slo/status`),
        fetch(`${API_BASE}/dashboard`),
        fetch(`${API_BASE}/metrics/history?duration=1h`)
      ])

      if (statusRes.ok) setStatus(await statusRes.json())
      if (metricsRes.ok) setMetrics(await metricsRes.json())
      if (sloRes.ok) setSloStatus(await sloRes.json())
      if (dashRes.ok) setDashboardData(await dashRes.json())

      if (historyRes.ok) {
        const history = await historyRes.json()
        processHistoryData(history)
      }

      setLoading(false)
      setError(null)
    } catch (err) {
      console.error('Dashboard fetch error:', err)
      setError(err.message)
      setLoading(false)
    }
  }

  const processHistoryData = (history) => {
    if (Array.isArray(history)) {
      // Process samples for CPU and memory charts
      const cpuData = []
      const memData = []

      history.slice(-60).forEach(sample => {
        const timestamp = new Date(sample.timestamp).toLocaleTimeString()
        let cpuVal = 0, memVal = 0

        Object.entries(sample.metrics || {}).forEach(([key, value]) => {
          if (key.includes('cpu') && key.includes('usage')) {
            cpuVal = value
          }
          if (key.includes('memory') && (key.includes('used') || key.includes('percent'))) {
            memVal = Math.max(memVal, value)
          }
        })

        cpuData.push({ time: timestamp, cpu: cpuVal })
        memData.push({ time: timestamp, memory: memVal })
      })

      setCpuHistory(cpuData)
      setMemHistory(memData)
    }
  }

  const getMetric = (name) => {
    // Find first matching metric
    const m = metrics.find(m => m.name === name)
    return m?.value || 0
  }

  // Get sum of all metrics with a given name (useful for labeled metrics)
  const getMetricSum = (name) => {
    return metrics
      .filter(m => m.name === name)
      .reduce((sum, m) => sum + (m.value || 0), 0)
  }

  const calculateMemoryUsage = () => {
    const used = getMetric('system.memory.used')
    const total = getMetric('system.memory.total')
    if (total > 0) return (used / total) * 100
    const available = getMetric('system.memory.available')
    const totalMem = getMetric('system.memory.total')
    if (totalMem > 0) return ((totalMem - available) / totalMem) * 100
    return 0
  }

  const formatBytes = (bytes) => {
    if (!bytes || bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
  }

  const formatUptime = (nanos) => {
    if (!nanos) return 'Unknown'
    const seconds = nanos / 1e9
    const days = Math.floor(seconds / 86400)
    const hours = Math.floor((seconds % 86400) / 3600)
    const mins = Math.floor((seconds % 3600) / 60)
    if (days > 0) return `${days}d ${hours}h`
    if (hours > 0) return `${hours}h ${mins}m`
    return `${mins}m`
  }

  const cpuUsage = getMetric('system.cpu.usage')
  const memUsage = calculateMemoryUsage()
  const load1 = getMetric('system.load.1m')
  const load5 = getMetric('system.load.5m')
  const load15 = getMetric('system.load.15m')
  const processCount = getMetric('system.processes')
  const procsRunning = getMetric('system.procs_running')
  const procsBlocked = getMetric('system.procs_blocked')
  const ioWait = getMetric('system.cpu.iowait')
  const cpuSteal = getMetric('system.cpu.steal')
  const networkRx = getMetricSum('system.network.bytes_recv')
  const networkTx = getMetricSum('system.network.bytes_sent')
  const diskReadBytes = getMetricSum('system.disk.read_bytes')
  const diskWriteBytes = getMetricSum('system.disk.write_bytes')
  const fdAllocated = getMetric('system.fd.allocated')
  const fdMax = getMetric('system.fd.maximum')
  const swapUsed = getMetric('system.swap.used')
  const swapTotal = getMetric('system.swap.total')

  const statsCards = [
    {
      title: 'CPU Usage',
      value: `${cpuUsage.toFixed(1)}%`,
      subtitle: ioWait > 5 ? `I/O Wait: ${ioWait.toFixed(1)}%` : `Steal: ${cpuSteal.toFixed(1)}%`,
      icon: Cpu,
      color: cpuUsage > 80 ? '#ef4444' : cpuUsage > 60 ? '#f59e0b' : '#10b981',
      trend: cpuUsage > 60 ? 'up' : 'stable',
      chart: cpuHistory.slice(-20)
    },
    {
      title: 'Memory Usage',
      value: `${memUsage.toFixed(1)}%`,
      subtitle: formatBytes(getMetric('system.memory.used')) + ' used',
      icon: HardDrive,
      color: memUsage > 80 ? '#ef4444' : memUsage > 60 ? '#f59e0b' : '#10b981',
      trend: memUsage > 60 ? 'up' : 'stable',
      chart: memHistory.slice(-20)
    },
    {
      title: 'System Load',
      value: load1.toFixed(2),
      subtitle: `5m: ${load5.toFixed(2)} | 15m: ${load15.toFixed(2)}`,
      icon: Activity,
      color: load1 > 4 ? '#ef4444' : load1 > 2 ? '#f59e0b' : '#10b981'
    },
    {
      title: 'Processes',
      value: processCount.toLocaleString(),
      subtitle: `Running: ${procsRunning} | Blocked: ${procsBlocked}`,
      icon: CheckCircle,
      color: procsBlocked > 5 ? '#ef4444' : '#6366f1'
    },
    {
      title: 'I/O Wait',
      value: `${ioWait.toFixed(1)}%`,
      subtitle: 'CPU waiting for I/O',
      icon: Clock,
      color: ioWait > 20 ? '#ef4444' : ioWait > 10 ? '#f59e0b' : '#10b981'
    },
    {
      title: 'Network Traffic',
      value: formatBytes(networkRx + networkTx),
      subtitle: `RX: ${formatBytes(networkRx)} | TX: ${formatBytes(networkTx)}`,
      icon: Network,
      color: '#8b5cf6'
    },
    {
      title: 'Disk I/O',
      value: formatBytes(diskReadBytes + diskWriteBytes),
      subtitle: `R: ${formatBytes(diskReadBytes)} | W: ${formatBytes(diskWriteBytes)}`,
      icon: HardDrive,
      color: '#ec4899'
    },
    {
      title: 'File Descriptors',
      value: fdAllocated.toLocaleString(),
      subtitle: fdMax > 0 ? `${((fdAllocated / fdMax) * 100).toFixed(1)}% of ${fdMax.toLocaleString()}` : 'Allocated',
      icon: Activity,
      color: fdMax > 0 && (fdAllocated / fdMax) > 0.8 ? '#ef4444' : '#6366f1'
    }
  ]

  const COLORS = ['#10b981', '#f59e0b', '#ef4444', '#6366f1', '#8b5cf6']

  return (
    <div className="dashboard-page">
      {loading && !status ? (
        <div className="loading-state">Loading dashboard...</div>
      ) : error ? (
        <div className="error-state">Error: {error}</div>
      ) : (
        <>
          {/* Summary Stats Cards */}
          <div className="stats-grid">
            {statsCards.map((stat, idx) => {
              const Icon = stat.icon
              return (
                <div key={idx} className="stat-card" style={{ '--card-color': stat.color }}>
                  <div className="stat-header">
                    <div className="stat-icon">
                      <Icon size={20} />
                    </div>
                    <div className="stat-trend">
                      {stat.trend === 'up' ? <TrendingUp size={16} /> : <Activity size={16} />}
                    </div>
                  </div>
                  <div className="stat-value">{stat.value}</div>
                  <div className="stat-title">{stat.title}</div>
                  {stat.subtitle && <div className="stat-subtitle">{stat.subtitle}</div>}
                  {stat.chart && (
                    <div className="mini-chart">
                      <ResponsiveContainer width="100%" height={40}>
                        <AreaChart data={stat.chart}>
                          <Area type="monotone" dataKey="cpu" stroke={stat.color} fill={stat.color} fillOpacity={0.3} />
                          <Area type="monotone" dataKey="memory" stroke={stat.color} fill={stat.color} fillOpacity={0.3} />
                        </AreaChart>
                      </ResponsiveContainer>
                    </div>
                  )}
                </div>
              )
            })}
          </div>

          {/* Status Banner */}
          {status && (
            <div className={`status-banner ${status.state === 'running' ? 'healthy' : 'unhealthy'}`}>
              {status.state === 'running' ? (
                <><CheckCircle size={18} /> Agent Running • Uptime: {formatUptime(status.uptime)}</>
              ) : (
                <><XCircle size={18} /> Agent {status.state}</>
              )}
            </div>
          )}

          {/* Charts Row */}
          <div className="charts-row">
            {/* CPU History Chart */}
            <div className="chart-card">
              <h3><Activity size={18} /> CPU Usage History</h3>
              <ResponsiveContainer width="100%" height={200}>
                <AreaChart data={cpuHistory}>
                  <defs>
                    <linearGradient id="cpuGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#6366f1" stopOpacity={0.8} />
                      <stop offset="95%" stopColor="#6366f1" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#333" />
                  <XAxis dataKey="time" stroke="#888" fontSize={12} />
                  <YAxis stroke="#888" fontSize={12} domain={[0, 100]} />
                  <Tooltip
                    contentStyle={{ backgroundColor: '#1e1e1e', border: '1px solid #333' }}
                    labelStyle={{ color: '#fff' }}
                  />
                  <Area type="monotone" dataKey="cpu" stroke="#6366f1" fillOpacity={1} fill="url(#cpuGradient)" />
                </AreaChart>
              </ResponsiveContainer>
            </div>

            {/* Memory History Chart */}
            <div className="chart-card">
              <h3><HardDrive size={18} /> Memory Usage History</h3>
              <ResponsiveContainer width="100%" height={200}>
                <AreaChart data={memHistory}>
                  <defs>
                    <linearGradient id="memGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#10b981" stopOpacity={0.8} />
                      <stop offset="95%" stopColor="#10b981" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#333" />
                  <XAxis dataKey="time" stroke="#888" fontSize={12} />
                  <YAxis stroke="#888" fontSize={12} domain={[0, 100]} />
                  <Tooltip
                    contentStyle={{ backgroundColor: '#1e1e1e', border: '1px solid #333' }}
                    labelStyle={{ color: '#fff' }}
                  />
                  <Area type="monotone" dataKey="memory" stroke="#10b981" fillOpacity={1} fill="url(#memGradient)" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </div>

          {/* SLO and Incident Summary */}
          <div className="summary-row">
            {/* SLO Status */}
            {sloStatus && Object.keys(sloStatus).length > 0 && (
              <div className="summary-card slo-card">
                <h3><CheckCircle size={18} /> SLO Status</h3>
                <div className="slo-list">
                  {Object.entries(sloStatus).slice(0, 3).map(([sloId, slo]) => (
                    <div key={sloId} className="slo-item">
                      <div className="slo-name">{sloId}</div>
                      <div className="slo-values">
                        <span className={`slo-value ${slo.compliant ? 'good' : 'bad'}`}>
                          {slo.current_value?.toFixed(2)}% / {slo.target}%
                        </span>
                        <span className={`error-budget ${slo.error_budget_status === 'ok' ? 'good' : 'warning'}`}>
                          Budget: {slo.error_budget_remaining?.toFixed(1)}%
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Active Incidents */}
            <div className="summary-card incidents-card">
              <h3><AlertTriangle size={18} /> Active Incidents</h3>
              {dashboardData?.active_incidents?.length > 0 ? (
                <div className="incident-list">
                  {dashboardData.active_incidents.slice(0, 4).map(inc => (
                    <div key={inc.id} className={`incident-item severity-${inc.severity?.toLowerCase()}`}>
                      <span className="incident-severity">{inc.severity}</span>
                      <span className="incident-title">{inc.title}</span>
                      <span className="incident-state">{inc.state}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="no-incidents">
                  <CheckCircle size={32} />
                  <p>No active incidents</p>
                </div>
              )}
            </div>

            {/* Changes Summary */}
            <div className="summary-card changes-card">
              <h3><Clock size={18} /> Change Summary</h3>
              {dashboardData?.change_stats && (
                <div className="change-stats">
                  <div className="change-stat">
                    <span className="stat-label">Planned</span>
                    <span className="stat-value">{dashboardData.change_stats.planned || 0}</span>
                  </div>
                  <div className="change-stat">
                    <span className="stat-label">In Progress</span>
                    <span className="stat-value active">{dashboardData.change_stats.in_progress || 0}</span>
                  </div>
                  <div className="change-stat">
                    <span className="stat-label">Completed</span>
                    <span className="stat-value">{dashboardData.change_stats.completed || 0}</span>
                  </div>
                  <div className="change-stat">
                    <span className="stat-label">Rolled Back</span>
                    <span className="stat-value error">{dashboardData.change_stats.rolled_back || 0}</span>
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* Quick Metrics */}
          <div className="quick-metrics">
            <h3>Quick Metrics</h3>
            <div className="metrics-grid">
              {metrics.slice(0, 12).map((metric, idx) => (
                <div key={idx} className="quick-metric">
                  <span className="metric-name">{metric.name.replace('system.', '')}</span>
                  <span className="metric-value">{metric.value?.toFixed(2)}</span>
                </div>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

export default Dashboard

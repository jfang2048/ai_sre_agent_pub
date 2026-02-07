import { useState, useEffect } from 'react'
import { Cpu, HardDrive, Network, Activity, Database } from 'lucide-react'
import {
  LineChart, Line, AreaChart, Area, XAxis, YAxis, CartesianGrid,
  Tooltip, ResponsiveContainer
} from 'recharts'
import MetricsDetailPanel from '../components/MetricsDetailPanel'
import './Metrics.css'

const API_BASE = '/api/v1'

function Metrics() {
  const [metrics, setMetrics] = useState([])
  const [history, setHistory] = useState(null)
  const [loading, setLoading] = useState(true)
  const [selectedCategory, setSelectedCategory] = useState('all')

  useEffect(() => {
    fetchMetrics()
    const interval = setInterval(fetchMetrics, 2000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    fetchHistory()
    const interval = setInterval(fetchHistory, 10000)
    return () => clearInterval(interval)
  }, [])

  const fetchMetrics = async () => {
    try {
      const res = await fetch(`${API_BASE}/metrics`)
      if (res.ok) {
        const data = await res.json()
        setMetrics(data)
        setLoading(false)
      }
    } catch (err) {
      console.error('Failed to fetch metrics:', err)
    }
  }

  const fetchHistory = async () => {
    try {
      const res = await fetch(`${API_BASE}/metrics/history?duration=1h`)
      if (res.ok) {
        const data = await res.json()
        setHistory(data)
      }
    } catch (err) {
      console.error('Failed to fetch history:', err)
    }
  }

  const formatValue = (metric) => {
    const val = metric.value
    if (metric.name.includes('percent') || metric.name.includes('usage')) {
      return `${val.toFixed(1)}%`
    }
    if (metric.name.includes('bytes') || metric.name.includes('memory')) {
      return formatBytes(val)
    }
    if (metric.name.includes('load')) {
      return val.toFixed(2)
    }
    if (metric.name.includes('time_ms')) {
      return `${val.toFixed(0)} ms`
    }
    return val.toLocaleString()
  }

  const formatBytes = (bytes) => {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
  }

  const getMetricIcon = (name) => {
    if (name.includes('cpu') || name.includes('load')) return Cpu
    if (name.includes('memory') || name.includes('swap')) return HardDrive
    if (name.includes('net')) return Network
    if (name.includes('disk')) return Database
    return Activity
  }

  const categories = [
    { id: 'all', name: 'All Metrics', filter: () => true },
    { id: 'cpu', name: 'CPU & Load', icon: Cpu, filter: m => m.name.includes('cpu') || m.name.includes('load') },
    { id: 'memory', name: 'Memory & Swap', icon: HardDrive, filter: m => m.name.includes('memory') || m.name.includes('swap') },
    { id: 'network', name: 'Network I/O', icon: Network, filter: m => m.name.includes('net.') },
    { id: 'disk', name: 'Disk I/O', icon: Database, filter: m => m.name.includes('disk.') },
    { id: 'system', name: 'System', icon: Activity, filter: m => m.name.includes('procs') || m.name.includes('fd.') || m.name.includes('processes') || m.name.includes('ctxt') },
    { id: 'detailed', name: 'Detailed Analysis', icon: Activity, filter: () => true }
  ]

  const activeCategory = categories.find(c => c.id === selectedCategory) || categories[0]
  const filteredMetrics = selectedCategory === 'all' ? metrics : metrics.filter(activeCategory.filter)

  // Group metrics by base name (excluding labels)
  const groupedMetrics = {}
  filteredMetrics.forEach(metric => {
    const baseName = metric.name
    if (!groupedMetrics[baseName]) {
      groupedMetrics[baseName] = []
    }
    groupedMetrics[baseName].push(metric)
  })

  // Process history data for charts
  const chartData = history && Array.isArray(history)
    ? history.slice(-60).map(sample => ({
      time: new Date(sample.timestamp).toLocaleTimeString(),
      ...sample.metrics
    }))
    : []

  return (
    <div className="metrics-page">
      <div className="metrics-header">
        <div className="category-tabs">
          {categories.map(cat => {
            const Icon = cat.icon || Activity
            return (
              <button
                key={cat.id}
                className={`category-tab ${selectedCategory === cat.id ? 'active' : ''}`}
                onClick={() => setSelectedCategory(cat.id)}
              >
                {cat.icon && <Icon size={16} />}
                <span>{cat.name}</span>
              </button>
            )
          })}
        </div>
      </div>

      {/* Chart for selected category */}
      {selectedCategory !== 'all' && chartData.length > 0 && (
        <div className="metrics-chart-card">
          <h3>{activeCategory.name} - Last 60 Samples</h3>
          <ResponsiveContainer width="100%" height={200}>
            <AreaChart data={chartData}>
              <CartesianGrid strokeDasharray="3 3" stroke="#333" />
              <XAxis dataKey="time" stroke="#888" fontSize={12} />
              <YAxis stroke="#888" fontSize={12} />
              <Tooltip
                contentStyle={{ backgroundColor: '#1e1e1e', border: '1px solid #333' }}
                labelStyle={{ color: '#fff' }}
              />
              {selectedCategory === 'cpu' && (
                <Area type="monotone" dataKey="system.cpu.usage" stroke="#6366f1" fill="#6366f1" fillOpacity={0.3} />
              )}
              {selectedCategory === 'memory' && (
                <>
                  <Area type="monotone" dataKey="system.memory.used" stroke="#10b981" fill="#10b981" fillOpacity={0.3} name="Used" />
                  <Area type="monotone" dataKey="system.memory.cached" stroke="#6366f1" fill="#6366f1" fillOpacity={0.3} name="Cached" />
                </>
              )}
              {selectedCategory === 'network' && (
                <>
                  <Area type="monotone" dataKey="system.net.rx_bytes" stroke="#10b981" fill="#10b981" fillOpacity={0.3} name="RX" />
                  <Area type="monotone" dataKey="system.net.tx_bytes" stroke="#6366f1" fill="#6366f1" fillOpacity={0.3} name="TX" />
                </>
              )}
              {selectedCategory === 'system' && (
                <Area type="monotone" dataKey="system.procs_running" stroke="#f59e0b" fill="#f59e0b" fillOpacity={0.3} />
              )}
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}

      {/* Detailed Analysis View */}
      {selectedCategory === 'detailed' ? (
        <div className="detailed-analysis-container">
          <MetricsDetailPanel metrics={metrics} />
        </div>
      ) : (
        /* Metrics Grid */
        <div className="metrics-grid">
          {loading ? (
            <div className="loading-message">Loading metrics...</div>
          ) : Object.keys(groupedMetrics).length === 0 ? (
            <div className="empty-state">No metrics found for this category</div>
          ) : (
            Object.entries(groupedMetrics).map(([name, metricList]) => {
              const Icon = getMetricIcon(name)
              return (
                <div key={name} className="metric-card">
                  <div className="metric-card-header">
                    <Icon size={16} />
                    <span className="metric-card-name">{name.replace('system.', '')}</span>
                  </div>
                  <div className="metric-values">
                    {metricList.map((metric, idx) => {
                      const labels = Object.entries(metric.labels || {})
                      return (
                        <div key={idx} className="metric-value-row">
                          <span className="metric-labels">
                            {labels.map(([k, v]) => `${k}=${v}`).join(' ')}
                          </span>
                          <span className="metric-value-text">{formatValue(metric)}</span>
                        </div>
                      )
                    })}
                  </div>
                </div>
              )
            })
          )}
        </div>
      )}

      {/* Raw Data Toggle */}
      <details className="raw-data-section">
        <summary>View Raw JSON Data</summary>
        <pre className="raw-data">
          {JSON.stringify(filteredMetrics, null, 2)}
        </pre>
      </details>
    </div>
  )
}

export default Metrics

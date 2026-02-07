import { useState, useEffect } from 'react'
import { Cpu, HardDrive, Activity, Search, ArrowUpDown } from 'lucide-react'
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import './Processes.css'

const API_BASE = '/api/v1'

function Processes() {
  const [metrics, setMetrics] = useState([])
  const [loading, setLoading] = useState(true)
  const [sortField, setSortField] = useState('cpu')
  const [sortOrder, setSortOrder] = useState('desc')
  const [searchQuery, setSearchQuery] = useState('')

  useEffect(() => {
    fetchMetrics()
    const interval = setInterval(fetchMetrics, 2000)
    return () => clearInterval(interval)
  }, [])

  const fetchMetrics = async () => {
    try {
      const res = await fetch(`${API_BASE}/metrics`)
      if (res.ok) {
        const data = await res.json()
        setMetrics(data || [])
        setLoading(false)
      }
    } catch (err) {
      console.error('Failed to fetch metrics:', err)
    }
  }

  const formatBytes = (bytes) => {
    if (!bytes || bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
  }

  // Get process metrics grouped by PID
  const getProcessData = () => {
    const processMap = new Map()

    metrics.forEach(m => {
      const pid = m.labels?.pid
      if (!pid) return

      if (!processMap.has(pid)) {
        processMap.set(pid, {
          pid,
          name: m.labels?.name || 'unknown',
          cpu: 0,
          memory: 0,
          readBytes: 0,
          writeBytes: 0,
          state: m.labels?.state || '-',
          wchan: m.labels?.wchan || '-'
        })
      }

      const proc = processMap.get(pid)
      if (m.name === 'process.cpu.percent') proc.cpu = m.value
      if (m.name === 'process.memory.bytes') proc.memory = m.value
      if (m.name === 'process.io.read_bytes') proc.readBytes = m.value
      if (m.name === 'process.io.write_bytes') proc.writeBytes = m.value
    })

    return Array.from(processMap.values())
  }

  const processData = getProcessData()

  // Filter by search
  const filteredData = searchQuery
    ? processData.filter(p =>
        p.pid.includes(searchQuery) ||
        p.name.toLowerCase().includes(searchQuery.toLowerCase())
      )
    : processData

  // Sort
  const sortedData = [...filteredData].sort((a, b) => {
    const aVal = a[sortField]
    const bVal = b[sortField]
    return sortOrder === 'asc' ? aVal - bVal : bVal - aVal
  })

  // Top 20 for display
  const displayData = sortedData.slice(0, 20)

  // Prepare chart data (top 10 by CPU)
  const chartData = sortedData.slice(0, 10).map(p => ({
    name: p.name.length > 15 ? p.name.substring(0, 15) + '...' : p.name,
    cpu: p.cpu,
    memory: p.memory / (1024 * 1024) // Convert to MB
  }))

  const handleSort = (field) => {
    if (sortField === field) {
      setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc')
    } else {
      setSortField(field)
      setSortOrder('desc')
    }
  }

  const SortIcon = ({ field }) => (
    <ArrowUpDown
      size={14}
      className={sortField === field ? 'active' : ''}
      style={{ opacity: sortField === field ? 1 : 0.3 }}
    />
  )

  const totalProcesses = metrics.find(m => m.name === 'system.processes')?.value || 0
  const totalThreads = metrics.find(m => m.name === 'system.processes.threads')?.value || 0
  const runningProcs = metrics.find(m => m.name === 'system.procs_running')?.value || 0
  const blockedProcs = metrics.find(m => m.name === 'system.procs_blocked')?.value || 0

  return (
    <div className="processes-page">
      {/* Summary Stats */}
      <div className="process-stats">
        <div className="stat-card">
          <Cpu size={20} className="stat-icon" />
          <div className="stat-value">{totalProcesses.toLocaleString()}</div>
          <div className="stat-label">Total Processes</div>
        </div>
        <div className="stat-card">
          <Activity size={20} className="stat-icon" />
          <div className="stat-value">{runningProcs}</div>
          <div className="stat-label">Running</div>
        </div>
        <div className="stat-card">
          <HardDrive size={20} className="stat-icon" />
          <div className="stat-value">{blockedProcs}</div>
          <div className="stat-label">Blocked (I/O)</div>
        </div>
        <div className="stat-card">
          <Activity size={20} className="stat-icon" />
          <div className="stat-value">{totalThreads.toLocaleString()}</div>
          <div className="stat-label">Total Threads</div>
        </div>
      </div>

      {/* Chart */}
      {chartData.length > 0 && (
        <div className="process-chart">
          <h3>Top 10 Processes by CPU Usage</h3>
          <ResponsiveContainer width="100%" height={250}>
            <BarChart data={chartData}>
              <CartesianGrid strokeDasharray="3 3" stroke="#333" />
              <XAxis dataKey="name" stroke="#888" fontSize={12} />
              <YAxis stroke="#888" fontSize={12} />
              <Tooltip
                contentStyle={{ backgroundColor: '#1e1e1e', border: '1px solid #333' }}
                labelStyle={{ color: '#fff' }}
              />
              <Bar dataKey="cpu" fill="#6366f1" name="CPU %" />
              <Bar dataKey="memory" fill="#10b981" name="Memory (MB)" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}

      {/* Search */}
      <div className="search-bar">
        <Search size={18} />
        <input
          type="text"
          placeholder="Search by PID or process name..."
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
        />
        <span className="result-count">{displayData.length} processes</span>
      </div>

      {/* Process Table */}
      {loading ? (
        <div className="loading-state">Loading process data...</div>
      ) : displayData.length === 0 ? (
        <div className="empty-state">No process data available</div>
      ) : (
        <div className="process-table-container">
          <table className="process-table">
            <thead>
              <tr>
                <th onClick={() => handleSort('pid')} className="sortable">
                  PID <SortIcon field="pid" />
                </th>
                <th onClick={() => handleSort('name')} className="sortable">
                  Name <SortIcon field="name" />
                </th>
                <th onClick={() => handleSort('cpu')} className="sortable">
                  CPU % <SortIcon field="cpu" />
                </th>
                <th onClick={() => handleSort('memory')} className="sortable">
                  Memory <SortIcon field="memory" />
                </th>
                <th onClick={() => handleSort('readBytes')} className="sortable">
                  Read/s <SortIcon field="readBytes" />
                </th>
                <th onClick={() => handleSort('writeBytes')} className="sortable">
                  Write/s <SortIcon field="writeBytes" />
                </th>
                <th>State</th>
                <th>Wchan</th>
              </tr>
            </thead>
            <tbody>
              {displayData.map((proc, idx) => (
                <tr key={proc.pid}>
                  <td className="font-mono">{proc.pid}</td>
                  <td className="proc-name" title={proc.name}>
                    {proc.name.length > 30 ? proc.name.substring(0, 30) + '...' : proc.name}
                  </td>
                  <td>
                    <div className="progress-cell">
                      <div className="progress-bar">
                        <div
                          className="progress-fill cpu"
                          style={{ width: `${Math.min(proc.cpu, 100)}%` }}
                        />
                      </div>
                      <span className="progress-value">{proc.cpu.toFixed(1)}%</span>
                    </div>
                  </td>
                  <td>{formatBytes(proc.memory)}</td>
                  <td>{formatBytes(proc.readBytes)}/s</td>
                  <td>{formatBytes(proc.writeBytes)}/s</td>
                  <td className="state-cell">{proc.state}</td>
                  <td className="font-mono wchan-cell" title={proc.wchan}>
                    {proc.wchan && proc.wchan !== '-' && proc.wchan !== '0'
                      ? (proc.wchan.length > 15 ? proc.wchan.substring(0, 15) + '...' : proc.wchan)
                      : '-'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Legend */}
      <div className="legend">
        <h4>Process States</h4>
        <div className="legend-items">
          <div className="legend-item">
            <span className="state-badge R">R</span>
            <span>Running</span>
          </div>
          <div className="legend-item">
            <span className="state-badge S">S</span>
            <span>Sleeping</span>
          </div>
          <div className="legend-item">
            <span className="state-badge D">D</span>
            <span>Waiting (Disk)</span>
          </div>
          <div className="legend-item">
            <span className="state-badge Z">Z</span>
            <span>Zombie</span>
          </div>
          <div className="legend-item">
            <span className="state-badge T">T</span>
            <span>Stopped</span>
          </div>
        </div>
      </div>
    </div>
  )
}

export default Processes

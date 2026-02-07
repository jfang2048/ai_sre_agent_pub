import { useState, useMemo } from 'react'
import { Search, ArrowUpDown, Filter, Cpu, HardDrive, Activity } from 'lucide-react'
import './ProcessPanel.css'

function ProcessPanel({ metrics }) {
  const [searchQuery, setSearchQuery] = useState('')
  const [sortField, setSortField] = useState('cpu')
  const [sortOrder, setSortOrder] = useState('desc')
  const [stateFilter, setStateFilter] = useState('all')
  const [showOnlyActive, setShowOnlyActive] = useState(true)

  const processMetrics = metrics.filter(m => m.name.startsWith('process.'))

  const getMetric = (name) => {
    const metric = processMetrics.find(m => m.name === name)
    return metric?.value || 0
  }

  const totalProcesses = getMetric('process.count')
  const totalThreads = getMetric('process.threads')
  const totalForks = getMetric('process.forks_total')

  // Get per-process metrics
  const perProcessMetrics = metrics.filter(m =>
    m.name.startsWith('process.cpu.percent') && m.labels?.pid
  )

  // Build process data
  const processes = useMemo(() => {
    return perProcessMetrics.map(m => {
      const pid = m.labels?.pid
      if (!pid) return null

      const memMetric = metrics.find(x => x.name === 'process.memory.bytes' && x.labels?.pid === pid)
      const readMetric = metrics.find(x => x.name === 'process.io.read_bytes' && x.labels?.pid === pid)
      const writeMetric = metrics.find(x => x.name === 'process.io.write_bytes' && x.labels?.pid === pid)
      const openFilesMetric = metrics.find(x => x.name === 'process.open_files' && x.labels?.pid === pid)
      const infoMetric = metrics.find(x => x.name === 'process.info' && x.labels?.pid === pid)

      return {
        pid: pid,
        name: m.labels?.name || 'unknown',
        cpu: m.value || 0,
        mem: memMetric?.value || 0,
        readRate: readMetric?.value || 0,
        writeRate: writeMetric?.value || 0,
        openFiles: openFilesMetric?.value || 0,
        wchan: infoMetric?.labels?.wchan || '-',
        state: infoMetric?.labels?.state || '-'
      }
    }).filter(Boolean)
  }, [metrics, perProcessMetrics])

  // Filter processes
  const filteredProcesses = useMemo(() => {
    return processes.filter(proc => {
      // Filter by search query
      if (searchQuery) {
        const query = searchQuery.toLowerCase()
        if (!proc.name.toLowerCase().includes(query) && !proc.pid.includes(query)) {
          return false
        }
      }

      // Filter by state
      if (stateFilter !== 'all' && proc.state !== stateFilter) {
        return false
      }

      // Show only active processes
      if (showOnlyActive && proc.cpu === 0 && proc.readRate === 0 && proc.writeRate === 0) {
        return false
      }

      return true
    })
  }, [processes, searchQuery, stateFilter, showOnlyActive])

  // Sort processes
  const sortedProcesses = useMemo(() => {
    return [...filteredProcesses].sort((a, b) => {
      const aVal = a[sortField]
      const bVal = b[sortField]

      if (typeof aVal === 'string' && typeof bVal === 'string') {
        return sortOrder === 'asc' ? aVal.localeCompare(bVal) : bVal.localeCompare(aVal)
      }

      return sortOrder === 'asc' ? aVal - bVal : bVal - aVal
    })
  }, [filteredProcesses, sortField, sortOrder])

  // Get process state counts
  const stateCounts = useMemo(() => {
    const counts = {}
    processes.forEach(p => {
      counts[p.state] = (counts[p.state] || 0) + 1
    })
    return counts
  }, [processes])

  const handleSort = (field) => {
    if (sortField === field) {
      setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc')
    } else {
      setSortField(field)
      setSortOrder('desc')
    }
  }

  const getStateColor = (state) => {
    const colors = {
      'R': 'running',
      'S': 'sleeping',
      'D': 'waiting',
      'Z': 'zombie',
      'T': 'stopped',
      'I': 'idle',
      '-': 'unknown'
    }
    return colors[state] || 'unknown'
  }

  const getCpuColor = (cpu) => {
    if (cpu >= 50) return 'critical'
    if (cpu >= 20) return 'high'
    if (cpu >= 5) return 'medium'
    return 'low'
  }

  const getMemColor = (mem) => {
    const gb = mem / (1024 * 1024 * 1024)
    if (gb >= 1) return 'critical'
    if (gb >= 0.5) return 'high'
    if (gb >= 0.1) return 'medium'
    return 'low'
  }

  return (
    <div className="process-panel glass-panel">
      <div className="glass-header">
        <h2>Process Level Insights</h2>
        <div className="header-badge">Low-Level Kernel Data</div>
      </div>

      {/* Summary Stats */}
      <div className="process-summary">
        <div className="process-stat">
          <Cpu size={16} className="stat-icon" />
          <div className="stat-content">
            <span className="process-stat-value">{totalProcesses.toLocaleString()}</span>
            <span className="process-stat-label">Processes</span>
          </div>
        </div>
        <div className="process-stat">
          <Activity size={16} className="stat-icon" />
          <div className="stat-content">
            <span className="process-stat-value">{totalThreads.toLocaleString()}</span>
            <span className="process-stat-label">Threads</span>
          </div>
        </div>
        <div className="process-stat">
          <HardDrive size={16} className="stat-icon" />
          <div className="stat-content">
            <span className="process-stat-value">{totalForks.toLocaleString()}</span>
            <span className="process-stat-label">Total Forks</span>
          </div>
        </div>
        <div className="process-stat">
          <div className="stat-content">
            <span className="process-stat-value">{sortedProcesses.length}</span>
            <span className="process-stat-label">Shown</span>
          </div>
        </div>
      </div>

      {/* Controls */}
      <div className="process-controls">
        <div className="search-box">
          <Search size={16} />
          <input
            type="text"
            placeholder="Search by PID or process name..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>

        <div className="filter-group">
          <Filter size={16} />
          <select
            value={stateFilter}
            onChange={(e) => setStateFilter(e.target.value)}
          >
            <option value="all">All States</option>
            {Object.entries(stateCounts).map(([state, count]) => (
              <option key={state} value={state}>
                {state} - {count}
              </option>
            ))}
          </select>

          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={showOnlyActive}
              onChange={(e) => setShowOnlyActive(e.target.checked)}
            />
            Active only
          </label>
        </div>

        <div className="results-count">
          {sortedProcesses.length} of {processes.length} processes
        </div>
      </div>

      {/* Process Table */}
      {sortedProcesses.length > 0 ? (
        <div className="top-processes">
          <div className="table-container">
            <table className="process-table-modern">
              <thead>
                <tr>
                  <th className="sortable" onClick={() => handleSort('pid')}>
                    PID <SortIcon field="pid" active={sortField === 'pid'} order={sortOrder} />
                  </th>
                  <th className="sortable" onClick={() => handleSort('name')}>
                    Application <SortIcon field="name" active={sortField === 'name'} order={sortOrder} />
                  </th>
                  <th className="sortable" onClick={() => handleSort('state')}>
                    State <SortIcon field="state" active={sortField === 'state'} order={sortOrder} />
                  </th>
                  <th className="sortable" onClick={() => handleSort('cpu')}>
                    CPU % <SortIcon field="cpu" active={sortField === 'cpu'} order={sortOrder} />
                  </th>
                  <th className="sortable" onClick={() => handleSort('mem')}>
                    Memory <SortIcon field="mem" active={sortField === 'mem'} order={sortOrder} />
                  </th>
                  <th className="sortable" onClick={() => handleSort('readRate')}>
                    Read/s <SortIcon field="readRate" active={sortField === 'readRate'} order={sortOrder} />
                  </th>
                  <th className="sortable" onClick={() => handleSort('writeRate')}>
                    Write/s <SortIcon field="writeRate" active={sortField === 'writeRate'} order={sortOrder} />
                  </th>
                  <th>Wchan</th>
                </tr>
              </thead>
              <tbody>
                {sortedProcesses.map((proc) => (
                  <tr key={proc.pid} className={proc.cpu > 50 ? 'high-cpu' : ''}>
                    <td className="mono pid-cell">{proc.pid}</td>
                    <td className="proc-name" title={proc.name}>
                      <span className="name-truncated">{truncate(proc.name, 30)}</span>
                    </td>
                    <td className="state-cell">
                      <span className={`state-badge ${getStateColor(proc.state)}`}>{proc.state}</span>
                    </td>
                    <td className="cpu-cell">
                      <div className="metric-bar">
                        <div className={`bar-fill cpu-${getCpuColor(proc.cpu)}`} style={{ width: `${Math.min(proc.cpu, 100)}%` }}></div>
                        <span className="bar-value">{proc.cpu.toFixed(1)}%</span>
                      </div>
                    </td>
                    <td className="memory-cell">
                      <div className="metric-bar">
                        <div className={`bar-fill mem-${getMemColor(proc.mem)}`} style={{ width: `${Math.min((proc.mem / (1024 * 1024 * 1024)) * 100, 100)}%` }}></div>
                        <span className="bar-value">{formatBytes(proc.mem)}</span>
                      </div>
                    </td>
                    <td className="io-cell">
                      {proc.readRate > 0 ? (
                        <span className="io-value">{formatBytes(proc.readRate)}/s</span>
                      ) : (
                        <span className="text-muted">-</span>
                      )}
                    </td>
                    <td className="io-cell">
                      {proc.writeRate > 0 ? (
                        <span className="io-value">{formatBytes(proc.writeRate)}/s</span>
                      ) : (
                        <span className="text-muted">-</span>
                      )}
                    </td>
                    <td className="wchan-cell mono" title={proc.wchan}>
                      {formatWchan(proc.wchan)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="empty-state">
          <Activity size={32} />
          <p>No processes found matching your filters</p>
        </div>
      )}

      {/* Legend */}
      <div className="process-legend">
        <div className="legend-section">
          <span className="legend-title">States:</span>
          {['R', 'S', 'D', 'Z', 'T', 'I'].map(state => (
            <span key={state} className={`state-badge ${getStateColor(state)}`} title={getStateName(state)}>
              {state}
            </span>
          ))}
        </div>
      </div>
    </div>
  )
}

function SortIcon({ field, active, order }) {
  return (
    <span className={`sort-icon ${active ? 'active' : ''}`}>
      {active && order === 'asc' ? '↑' : active && order === 'desc' ? '↓' : '↕'}
    </span>
  )
}

function formatBytes(bytes) {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function truncate(str, len) {
  if (!str) return ''
  return str.length > len ? str.substring(0, len) + '...' : str
}

function formatWchan(wchan) {
  if (!wchan || wchan === '0' || wchan === '-') return <span className="text-muted">-</span>
  if (wchan.length > 15) return <span title={wchan}>{wchan.substring(0, 15)}...</span>
  return wchan
}

function getStateName(state) {
  const names = {
    'R': 'Running',
    'S': 'Sleeping',
    'D': 'Waiting (Disk)',
    'Z': 'Zombie',
    'T': 'Stopped',
    'I': 'Idle'
  }
  return names[state] || 'Unknown'
}

export default ProcessPanel

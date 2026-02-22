import './StatusPanel.css'

function StatusPanel({ status }) {
  if (!status) {
    return (
      <div className="status-panel glass-panel">
        <div className="status-loading">Loading status...</div>
      </div>
    )
  }

  const uptime = (status.uptime !== undefined) ? formatUptime(status.uptime) : 'Unknown'

  return (
    <div className="status-panel glass-panel">
      <h2>Agent Status</h2>
      <div className="status-grid">
        <div className="status-item">
          <span className="status-label">State</span>
          <span className={`status-value ${status.state === 'running' ? 'running' : 'not-running'}`}>
            {status.state}
          </span>
        </div>
        <div className="status-item">
          <span className="status-label">Version</span>
          <span className="status-value">{status.version}</span>
        </div>
        <div className="status-item">
          <span className="status-label">Uptime</span>
          <span className="status-value">{uptime}</span>
        </div>
        <div className="status-item">
          <span className="status-label">Sources</span>
          <span className="status-value">{Object.keys(status.collector || {}).length}</span>
        </div>
      </div>
    </div>
  )
}

function formatUptime(nanos) {
  if (nanos === 0) return 'Just started'

  // Convert nanoseconds to seconds (Go time.Duration marshals as nanoseconds)
  const seconds = nanos / 1e9
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const mins = Math.floor((seconds % 3600) / 60)
  const secs = Math.floor(seconds % 60)

  if (days > 0) {
    return `${days}d ${hours}h ${mins}m`
  }
  if (hours > 0) {
    return `${hours}h ${mins}m`
  }
  if (mins > 0) {
    return `${mins}m ${secs}s`
  }
  return `${secs}s`
}

export default StatusPanel

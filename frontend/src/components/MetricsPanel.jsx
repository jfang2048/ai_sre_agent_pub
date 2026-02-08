import './MetricsPanel.css'

function MetricsPanel({ title, metrics, filter }) {
  const filteredMetrics = filter ? metrics.filter(filter) : metrics

  const formatValue = (metric) => {
    const val = metric.value
    if (metric.name.includes('percent') || metric.name.includes('usage') || metric.name.includes('_usage')) {
      return `${val.toFixed(1)}%`
    }
    if (metric.name.includes('bytes') || metric.name.includes('memory') || metric.name.includes('swap')) {
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

  // Group metrics by labels if present (e.g., interface, device)
  const renderMetric = (metric, idx) => {
    let labelSuffix = ''
    if (metric.labels) {
      if (metric.labels.interface) labelSuffix = `local [${metric.labels.interface}]`
      if (metric.labels.device) labelSuffix = `[${metric.labels.device}]`
      if (metric.labels.path) labelSuffix = `[${metric.labels.path}]`
    }

    return (
      <div key={idx} className="metric-row">
        <span className="metric-name" title={metric.name}>
          {metric.name.replace('system.', '').replace('disk.', '').replace('net.', '')}
          {labelSuffix && <span className="metric-label">{labelSuffix}</span>}
        </span>
        <span className="metric-value">{formatValue(metric)}</span>
      </div>
    )
  }

  return (
    <div className="metrics-panel glass-panel">
      <h3>{title}</h3>
      <div className="metrics-list">
        {filteredMetrics.length === 0 ? (
          <div className="metrics-empty">No metrics available</div>
        ) : (
          filteredMetrics.map(renderMetric)
        )}
      </div>
    </div>
  )
}

function formatBytes(bytes) {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

export default MetricsPanel

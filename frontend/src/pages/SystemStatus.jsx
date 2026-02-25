import { useState, useEffect } from 'react'
import {
  Server, Cpu, HardDrive, Network, Activity, CheckCircle, XCircle, AlertTriangle,
  Thermometer, Zap, Settings, Monitor, Container, Box, Globe,
  ChevronDown, ChevronUp, Info, HelpCircle
} from 'lucide-react'
import {
  LineChart, Line, AreaChart, Area, BarChart, Bar,
  XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer, PieChart, Pie, Cell
} from 'recharts'
import MetricsDetailPanel from '../components/MetricsDetailPanel'
import './SystemStatus.css'

const API_BASE = '/api/v1'

// Machine type constants
const MACHINE_TYPES = {
  0: { name: 'Bare Metal', icon: Server, color: '#10b981' },
  1: { name: 'Virtual Machine', icon: Box, color: '#6366f1' },
  2: { name: 'Container', icon: Container, color: '#f59e0b' },
  99: { name: 'Unknown', icon: HelpCircle, color: '#6b7280' }
}

function SystemStatus() {
  const [metrics, setMetrics] = useState([])
  const [loading, setLoading] = useState(true)
  const [expandedSections, setExpandedSections] = useState({
    overview: true,
    hardware: true,
    cpu: true,
    memory: true,
    disk: true,
    network: true,
    gpu: false,
    kubernetes: false,
    detailed_metrics: true  // Start expanded for visibility
  })

  useEffect(() => {
    fetchMetrics()
    const interval = setInterval(fetchMetrics, 5000)
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

  const getMetric = (name, labels = {}) => {
    const metric = metrics.find(m => m.name === name)
    if (!metric) return 0

    // If labels specified, find matching metric
    if (Object.keys(labels).length > 0) {
      const labeled = metrics.find(m => m.name === name &&
        Object.entries(labels).every(([k, v]) => m.labels?.[k] === v))
      return labeled?.value || 0
    }

    return metric.value || 0
  }

  const getMetricsByName = (name) => {
    return metrics.filter(m => m.name === name)
  }

  // Sum all metrics with the same name (for labeled metrics)
  const getMetricSum = (name) => {
    return metrics
      .filter(m => m.name === name)
      .reduce((sum, m) => sum + (m.value || 0), 0)
  }

  const getMetricLabel = (name, labelKey) => {
    const ms = getMetricsByName(name)
    return ms.map(m => m.labels?.[labelKey]).filter(Boolean)
  }

  const toggleSection = (section) => {
    setExpandedSections(prev => ({
      ...prev,
      [section]: !prev[section]
    }))
  }

  // Detect machine type
  const machineTypeValue = getMetric('hardware.machine.type')
  const machineType = MACHINE_TYPES[machineTypeValue] || MACHINE_TYPES[99]
  const MachineTypeIcon = machineType.icon

  // Hardware info
  const cpuModel = metrics.find(m => m.name === 'hardware.cpu.model')
  const cpuVendor = metrics.find(m => m.name === 'hardware.cpu.vendor')
  const cpuCores = getMetric('hardware.cpu.cores')
  const cpuThreads = getMetric('hardware.cpu.threads')
  const architecture = metrics.find(m => m.name === 'hardware.architecture')

  // System info
  const kernelVersion = metrics.find(m => m.name === 'hardware.kernel.version')
  const systemVendor = metrics.find(m => m.name === 'hardware.system.vendor')
  const productModel = metrics.find(m => m.name === 'hardware.system.product')
  const biosVendor = metrics.find(m => m.name === 'hardware.bios.vendor')

  // Memory info
  const memTotal = getMetric('system.memory.total')
  const memAvailable = getMetric('system.memory.available')
  const memUsed = memTotal - memAvailable
  const memPercent = memTotal > 0 ? (memUsed / memTotal) * 100 : 0

  // CPU usage
  const cpuUsage = getMetric('system.cpu.usage')

  // GPU info
  const gpuMetrics = metrics.filter(m => m.name?.startsWith('gpu.'))
  const hasGPU = gpuMetrics.length > 0
  const gpuCount = new Set(gpuMetrics.map(m => m.labels?.gpu_id)).size

  // Kubernetes info
  const k8sMember = getMetric('kubernetes.cluster.member') === 1
  const k8sPodCount = getMetric('kubernetes.node.pods.count')
  const k8sContainerCount = getMetric('kubernetes.node.containers.count')

  const formatBytes = (bytes) => {
    if (!bytes || bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
  }

  const formatMHz = (mhz) => {
    if (mhz >= 1000) return (mhz / 1000).toFixed(2) + ' GHz'
    return mhz.toFixed(0) + ' MHz'
  }

  const getHealthStatus = (value, warningThreshold, criticalThreshold) => {
    if (value >= criticalThreshold) return { status: 'critical', color: '#ef4444', icon: XCircle }
    if (value >= warningThreshold) return { status: 'warning', color: '#f59e0b', icon: AlertTriangle }
    return { status: 'healthy', color: '#10b981', icon: CheckCircle }
  }

  const cpuHealth = getHealthStatus(cpuUsage, 60, 85)
  const memHealth = getHealthStatus(memPercent, 70, 90)

  return (
    <div className="system-status-page">
      {/* Header */}
      <div className="status-header">
        <div className="header-title">
          <Server size={28} />
          <div>
            <h1>System Status</h1>
            <p className="subtitle">Real-time system health and performance monitoring</p>
          </div>
        </div>
        <div className="machine-type-badge" style={{ '--type-color': machineType.color }}>
          <MachineTypeIcon size={20} />
          <span>{machineType.name}</span>
        </div>
      </div>

      {/* Overview Cards */}
      <div className="overview-cards">
        <div className="overview-card cpu-card" style={{ '--card-color': cpuHealth.color }}>
          <div className="card-header">
            <Cpu size={24} />
            <span className="card-title">CPU</span>
            <cpuHealth.icon size={20} className="health-icon" />
          </div>
          <div className="card-value">{cpuUsage.toFixed(1)}%</div>
          <div className="card-subtitle">
            {cpuCores} cores / {cpuThreads} threads
          </div>
          <div className="progress-bar">
            <div className="progress-fill" style={{ width: `${Math.min(cpuUsage, 100)}%` }} />
          </div>
        </div>

        <div className="overview-card memory-card" style={{ '--card-color': memHealth.color }}>
          <div className="card-header">
            <HardDrive size={24} />
            <span className="card-title">Memory</span>
            <memHealth.icon size={20} className="health-icon" />
          </div>
          <div className="card-value">{memPercent.toFixed(1)}%</div>
          <div className="card-subtitle">
            {formatBytes(memUsed)} / {formatBytes(memTotal)}
          </div>
          <div className="progress-bar">
            <div className="progress-fill" style={{ width: `${Math.min(memPercent, 100)}%` }} />
          </div>
        </div>

        <div className="overview-card network-card">
          <div className="card-header">
            <Network size={24} />
            <span className="card-title">Network</span>
            <CheckCircle size={20} className="health-icon" />
          </div>
          <div className="card-value">{getMetricsByName('hardware.network.interface.up').length}</div>
          <div className="card-subtitle">Active interfaces</div>
        </div>

        {hasGPU && (
          <div className="overview-card gpu-card">
            <div className="card-header">
              <Monitor size={24} />
              <span className="card-title">GPU</span>
              <CheckCircle size={20} className="health-icon" />
            </div>
            <div className="card-value">{gpuCount}</div>
            <div className="card-subtitle">GPU(s) detected</div>
          </div>
        )}

        {k8sMember && (
          <div className="overview-card k8s-card">
            <div className="card-header">
              <Container size={24} />
              <span className="card-title">Kubernetes</span>
              <CheckCircle size={20} className="health-icon" />
            </div>
            <div className="card-value">{k8sPodCount || 0}</div>
            <div className="card-subtitle">Pods on this node</div>
          </div>
        )}
      </div>

      {/* Hardware Section */}
      <div className="status-section">
        <div className="section-header" onClick={() => toggleSection('hardware')}>
          <div className="section-title">
            <Settings size={20} />
            <span>Hardware Information</span>
          </div>
          {expandedSections.hardware ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
        </div>

        {expandedSections.hardware && (
          <div className="section-content">
            <div className="hardware-grid">
              <div className="hardware-item">
                <span className="hardware-label">CPU Model</span>
                <span className="hardware-value">{cpuModel?.labels?.model || 'Unknown'}</span>
              </div>
              <div className="hardware-item">
                <span className="hardware-label">CPU Vendor</span>
                <span className="hardware-value">{cpuVendor?.labels?.vendor || 'Unknown'}</span>
              </div>
              <div className="hardware-item">
                <span className="hardware-label">Architecture</span>
                <span className="hardware-value">{architecture?.labels?.arch || 'Unknown'}</span>
              </div>
              <div className="hardware-item">
                <span className="hardware-label">Kernel Version</span>
                <span className="hardware-value mono">{kernelVersion?.labels?.version || 'Unknown'}</span>
              </div>
              <div className="hardware-item">
                <span className="hardware-label">System Vendor</span>
                <span className="hardware-value">{systemVendor?.labels?.vendor || 'Unknown'}</span>
              </div>
              <div className="hardware-item">
                <span className="hardware-label">Product Model</span>
                <span className="hardware-value">{productModel?.labels?.product || 'Unknown'}</span>
              </div>
              {biosVendor && (
                <div className="hardware-item">
                  <span className="hardware-label">BIOS Vendor</span>
                  <span className="hardware-value">{biosVendor.labels?.vendor || 'Unknown'}</span>
                </div>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Storage Section */}
      <div className="status-section">
        <div className="section-header" onClick={() => toggleSection('disk')}>
          <div className="section-title">
            <HardDrive size={20} />
            <span>Storage Devices</span>
          </div>
          {expandedSections.disk ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
        </div>

        {expandedSections.disk && (
          <div className="section-content">
            <div className="disk-table">
              <table>
                <thead>
                  <tr>
                    <th>Device</th>
                    <th>Type</th>
                    <th>Size</th>
                    <th>Scheduler</th>
                  </tr>
                </thead>
                <tbody>
                  {getMetricsByName('hardware.disk.size_bytes').map(m => {
                    const device = m.labels?.device || ''
                    const typeMetric = metrics.find(x => x.name === 'hardware.disk.type' && x.labels?.device === device)
                    const schedMetric = metrics.find(x => x.name === 'hardware.disk.scheduler' && x.labels?.device === device)
                    return (
                      <tr key={device}>
                        <td className="mono">{device}</td>
                        <td>
                          <span className={`disk-type ${(typeMetric?.labels?.type || 'unknown').toLowerCase()}`}>
                            {typeMetric?.labels?.type || 'Unknown'}
                          </span>
                        </td>
                        <td>{formatBytes(m.value)}</td>
                        <td className="mono">{schedMetric?.labels?.scheduler || '-'}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>

      {/* Network Section */}
      <div className="status-section">
        <div className="section-header" onClick={() => toggleSection('network')}>
          <div className="section-title">
            <Network size={20} />
            <span>Network Interfaces</span>
          </div>
          {expandedSections.network ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
        </div>

        {expandedSections.network && (
          <div className="section-content">
            <div className="network-grid">
              {getMetricsByName('hardware.network.interface.up').map(m => {
                const iface = m.labels?.interface || ''
                const speed = metrics.find(x => x.name === 'hardware.network.speed_mbps' && x.labels?.interface === iface)
                const duplex = metrics.find(x => x.name === 'hardware.network.duplex' && x.labels?.interface === iface)
                const isUp = m.value === 1
                return (
                  <div key={iface} className="network-card">
                    <div className="network-header">
                      <span className="network-name mono">{iface}</span>
                      <span className={`network-status ${isUp ? 'up' : 'down'}`}>
                        {isUp ? 'UP' : 'DOWN'}
                      </span>
                    </div>
                    {isUp && speed && (
                      <div className="network-detail">
                        <Network size={14} />
                        <span>{speed.value >= 1000 ? `${speed.value / 1000} Gbps` : `${speed.value} Mbps`}</span>
                      </div>
                    )}
                    {isUp && duplex && (
                      <div className="network-detail">
                        <Activity size={14} />
                        <span>{duplex.labels?.duplex || 'Unknown'}</span>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        )}
      </div>

      {/* GPU Section */}
      {hasGPU && (
        <div className="status-section">
          <div className="section-header" onClick={() => toggleSection('gpu')}>
            <div className="section-title">
              <Monitor size={20} />
              <span>GPU Status</span>
            </div>
            {expandedSections.gpu ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
          </div>

          {expandedSections.gpu && (
            <div className="section-content">
              <div className="gpu-grid">
                {Array.from(new Set(gpuMetrics.filter(m => m.labels?.gpu_id).map(m => m.labels?.gpu_id))).map(gpuId => {
                  const gpuName = gpuMetrics.find(m => m.labels?.gpu_id === gpuId)?.labels?.name || `GPU ${gpuId}`
                  const gpuUtil = getMetric('gpu.nvidia.utilization.gpu', { gpu_id: gpuId })
                  const memUtil = getMetric('gpu.nvidia.utilization.memory', { gpu_id: gpuId })
                  const memTotal = getMetric('gpu.nvidia.memory.total', { gpu_id: gpuId })
                  const memUsed = getMetric('gpu.nvidia.memory.used', { gpu_id: gpuId })
                  const temperature = getMetric('gpu.nvidia.temperature.gpu', { gpu_id: gpuId })
                  const powerDraw = getMetric('gpu.nvidia.power.draw', { gpu_id: gpuId })
                  const powerLimit = getMetric('gpu.nvidia.power.limit', { gpu_id: gpuId })
                  const fanSpeed = getMetric('gpu.nvidia.fan.speed', { gpu_id: gpuId })

                  const tempHealth = getHealthStatus(temperature, 75, 85)

                  return (
                    <div key={gpuId} className="gpu-card">
                      <div className="gpu-header">
                        <Monitor size={20} />
                        <span className="gpu-name">{gpuName}</span>
                        <span className="gpu-id mono">ID: {gpuId}</span>
                      </div>

                      <div className="gpu-metrics">
                        <div className="gpu-metric">
                          <span className="metric-label">GPU Util</span>
                          <div className="metric-value">
                            <div className="progress-bar">
                              <div className="progress-fill gpu" style={{ width: `${gpuUtil}%` }} />
                            </div>
                            <span className="metric-number">{gpuUtil.toFixed(0)}%</span>
                          </div>
                        </div>

                        <div className="gpu-metric">
                          <span className="metric-label">Memory</span>
                          <div className="metric-value">
                            <div className="progress-bar">
                              <div className="progress-fill memory" style={{ width: `${memUtil}%` }} />
                            </div>
                            <span className="metric-number">{formatBytes(memUsed * 1024 * 1024)} / {formatBytes(memTotal * 1024 * 1024)}</span>
                          </div>
                        </div>

                        <div className="gpu-metric-row">
                          <div className="gpu-metric-small">
                            <Thermometer size={16} />
                            <span>{temperature.toFixed(0)}°C</span>
                          </div>
                          {powerDraw > 0 && (
                            <div className="gpu-metric-small">
                              <Zap size={16} />
                              <span>{powerDraw.toFixed(0)}W</span>
                            </div>
                          )}
                          {fanSpeed > 0 && (
                            <div className="gpu-metric-small">
                              <Activity size={16} />
                              <span>{fanSpeed.toFixed(0)}%</span>
                            </div>
                          )}
                        </div>
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Kubernetes Section */}
      {k8sMember && (
        <div className="status-section">
          <div className="section-header" onClick={() => toggleSection('kubernetes')}>
            <div className="section-title">
              <Container size={20} />
              <span>Kubernetes</span>
            </div>
            {expandedSections.kubernetes ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
          </div>

          {expandedSections.kubernetes && (
            <div className="section-content">
              <div className="k8s-info">
                <div className="k8s-metric">
                  <span className="k8s-label">Namespace</span>
                  <span className="k8s-value mono">{metrics.find(m => m.labels?.namespace)?.labels?.namespace || 'default'}</span>
                </div>
                <div className="k8s-metric">
                  <span className="k8s-label">Pod</span>
                  <span className="k8s-value mono">{metrics.find(m => m.labels?.pod)?.labels?.pod || 'N/A'}</span>
                </div>
                <div className="k8s-metric">
                  <span className="k8s-label">Node</span>
                  <span className="k8s-value mono">{metrics.find(m => m.labels?.node)?.labels?.node || 'N/A'}</span>
                </div>
              </div>

              {k8sPodCount > 0 && (
                <div className="k8s-pods">
                  <h4>Pods on this node: {k8sPodCount}</h4>
                  <div className="k8s-containers">Containers: {k8sContainerCount || 0}</div>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Detailed Metrics Panel */}
      <div className="status-section">
        <div className="section-header" onClick={() => toggleSection('detailed_metrics')}>
          <div className="section-title">
            <Activity size={20} />
            <span>Detailed Metrics Analysis</span>
          </div>
          {expandedSections.detailed_metrics ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
        </div>

        {expandedSections.detailed_metrics && (
          <div className="section-content">
            <MetricsDetailPanel metrics={metrics} />
          </div>
        )}
      </div>

      {/* Raw Data */}
      <details className="raw-data-section">
        <summary>View Raw Metrics Data</summary>
        <pre className="raw-data">
          {JSON.stringify(metrics, null, 2)}
        </pre>
      </details>
    </div>
  )
}

export default SystemStatus

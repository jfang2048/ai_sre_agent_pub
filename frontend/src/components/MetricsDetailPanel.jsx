import { useState, useMemo } from 'react'
import {
    Activity, Cpu, HardDrive, Network, Zap, Clock, Database,
    TrendingUp, AlertCircle, ChevronDown, ChevronUp, Info
} from 'lucide-react'
import { LineChart, Line, BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import './MetricsDetailPanel.css'

function MetricsDetailPanel({ metrics }) {
    const [expandedCategory, setExpandedCategory] = useState({
        cpu: true,
        memory: true,
        disk: true,
        network: true,
        kernel: false,
        io: false
    })

    const toggleCategory = (category) => {
        setExpandedCategory(prev => ({ ...prev, [category]: !prev[category] }))
    }

    // Helper to get metric value
    const getMetric = (name, labels = {}) => {
        const metric = metrics.find(m => m.name === name &&
            Object.entries(labels).every(([k, v]) => m.labels?.[k] === v))
        return metric?.value || 0
    }

    // Helper to get metrics by prefix
    const getMetricsByPrefix = (prefix) => {
        return metrics.filter(m => m.name?.startsWith(prefix))
    }

    // CPU Metrics
    const cpuMetrics = useMemo(() => ({
        usage: getMetric('system.cpu.usage'),
        user: getMetric('system.cpu.user'),
        system: getMetric('system.cpu.system'),
        idle: getMetric('system.cpu.idle'),
        iowait: getMetric('system.cpu.iowait'),
        irq: getMetric('system.cpu.irq'),
        softirq: getMetric('system.cpu.softirq'),
        steal: getMetric('system.cpu.steal'),
        guest: getMetric('system.cpu.guest'),
        cores: getMetric('hardware.cpu.cores'),
        threads: getMetric('hardware.cpu.threads'),
        contextSwitches: getMetric('system.cpu.context_switches'),
        interrupts: getMetric('system.cpu.interrupts'),
        // RCA Data
        topProcesses: metrics
            .filter(m => m.name === 'rca_cpu_process_percent')
            .sort((a, b) => b.value - a.value)
            .slice(0, 5)
            .map(m => ({
                pid: m.labels.pid,
                name: m.labels.name,
                value: m.value,
                user: getMetric('rca_cpu_process_user_percent', { pid: m.labels.pid }),
                sys: getMetric('rca_cpu_process_system_percent', { pid: m.labels.pid }),
                state: metrics.find(s => s.name === 'rca_cpu_process_state' && s.labels.pid === m.labels.pid && s.value === 1)?.labels?.state
            })),
        blockedProcesses: metrics
            .filter(m => m.name === 'rca_cpu_process_state' && m.labels.state === 'disk_wait' && m.value === 1)
            .map(m => ({
                pid: m.labels.pid,
                name: m.labels.name,
                wchan: metrics.find(w => w.name === 'rca_cpu_process_wchan' && w.labels.pid === m.labels.pid)?.labels?.wchan
            }))
    }), [metrics])

    // Memory Metrics
    const memoryMetrics = useMemo(() => ({
        total: getMetric('system.memory.total'),
        available: getMetric('system.memory.available'),
        used: getMetric('system.memory.used'),
        free: getMetric('system.memory.free'),
        cached: getMetric('system.memory.cached'),
        buffers: getMetric('system.memory.buffers'),
        swapTotal: getMetric('system.swap.total'),
        swapUsed: getMetric('system.swap.used'),
        swapFree: getMetric('system.swap.free'),
        pageFaults: getMetric('system.memory.page_faults'),
        majorPageFaults: getMetric('system.memory.major_page_faults'),
        slab: getMetric('system.memory.slab'),
        dirty: getMetric('system.memory.dirty'),
        writeback: getMetric('system.memory.writeback'),
        anon: getMetric('system.memory.anon'),
        mapped: getMetric('system.memory.mapped'),
        // RCA Data
        topProcesses: metrics
            .filter(m => m.name === 'rca_memory_process_rss_bytes')
            .sort((a, b) => b.value - a.value)
            .slice(0, 5)
            .map(m => ({
                pid: m.labels.pid,
                name: m.labels.name,
                value: m.value,
                percent: getMetric('rca_memory_process_percent', { pid: m.labels.pid }),
                swap: getMetric('rca_memory_process_swap_bytes', { pid: m.labels.pid }),
                oom: getMetric('rca_memory_process_oom_score', { pid: m.labels.pid })
            }))
    }), [metrics])

    // Disk I/O Metrics
    const diskMetrics = useMemo(() => {
        const disks = {}
        // Standard metrics
        getMetricsByPrefix('system.disk.').forEach(m => {
            const device = m.labels?.device
            if (!device) return
            if (!disks[device]) disks[device] = {}

            if (m.name.includes('read_bytes')) disks[device].readBytes = m.value
            if (m.name.includes('write_bytes')) disks[device].writeBytes = m.value
            if (m.name.includes('read_ops')) disks[device].readOps = m.value
            if (m.name.includes('write_ops')) disks[device].writeOps = m.value
            if (m.name.includes('io_time')) disks[device].ioTime = m.value
            if (m.name.includes('weighted_io_time')) disks[device].weightedIOTime = m.value
        })

        // RCA Metrics (Utilization, Latency)
        getMetricsByPrefix('rca_io_device_').forEach(m => {
            const device = m.labels?.device
            if (!device) return
            if (!disks[device]) disks[device] = {}

            if (m.name.includes('utilization_percent')) disks[device].utilization = m.value
            if (m.name.includes('avg_read_latency_ms')) disks[device].avgReadLatency = m.value
            if (m.name.includes('avg_write_latency_ms')) disks[device].avgWriteLatency = m.value
        })
        return disks
    }, [metrics])

    // Network Metrics - look for both prefixes (system.net. and system.network.)
    const networkMetrics = useMemo(() => {
        const interfaces = {}
        metrics.filter(m => m.name?.startsWith('system.net.') || m.name?.startsWith('system.network.')).forEach(m => {
            const iface = m.labels?.interface
            if (!iface) return
            if (!interfaces[iface]) interfaces[iface] = {}

            if (m.name.includes('rx_bytes') || m.name.includes('bytes_recv')) interfaces[iface].bytesRecv = m.value
            if (m.name.includes('tx_bytes') || m.name.includes('bytes_sent')) interfaces[iface].bytesSent = m.value
            if (m.name.includes('rx_packets') || m.name.includes('packets_recv')) interfaces[iface].packetsRecv = m.value
            if (m.name.includes('tx_packets') || m.name.includes('packets_sent')) interfaces[iface].packetsSent = m.value
            if (m.name.includes('rx_errors') || m.name.includes('errin')) interfaces[iface].errIn = m.value
            if (m.name.includes('tx_errors') || m.name.includes('errout')) interfaces[iface].errOut = m.value
            if (m.name.includes('rx_drops') || m.name.includes('dropin')) interfaces[iface].dropIn = m.value
            if (m.name.includes('tx_drops') || m.name.includes('dropout')) interfaces[iface].dropOut = m.value
        })
        return interfaces
    }, [metrics])

    // Kernel Metrics
    const kernelMetrics = useMemo(() => ({
        processesTotal: getMetric('system.processes'),
        processesRunning: getMetric('system.procs_running'),
        processesBlocked: getMetric('system.procs_blocked'),
        threadsTotal: getMetric('process.threads'),
        forksTotal: getMetric('process.forks_total'),
        contextSwitches: getMetric('system.ctxt'),
        interrupts: getMetric('system.cpu.interrupts'),
        fileDescriptors: getMetric('system.fd.allocated'),
        fileDescriptorsMax: getMetric('system.fd.maximum'),
        inodes: getMetric('system.inode.allocated'),
    }), [metrics])

    const formatBytes = (bytes) => {
        if (!bytes || bytes === 0) return '0 B'
        const k = 1024
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
        const i = Math.floor(Math.log(bytes) / Math.log(k))
        return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
    }

    const formatNumber = (num) => {
        if (num >= 1e9) return (num / 1e9).toFixed(2) + 'B'
        if (num >= 1e6) return (num / 1e6).toFixed(2) + 'M'
        if (num >= 1e3) return (num / 1e3).toFixed(2) + 'K'
        return num.toFixed(0)
    }

    const getHealthColor = (percent) => {
        if (percent >= 90) return 'critical'
        if (percent >= 75) return 'warning'
        if (percent >= 50) return 'moderate'
        return 'healthy'
    }

    const memUsedPercent = memoryMetrics.total > 0
        ? ((memoryMetrics.total - memoryMetrics.available) / memoryMetrics.total) * 100
        : 0

    const swapUsedPercent = memoryMetrics.swapTotal > 0
        ? (memoryMetrics.swapUsed / memoryMetrics.swapTotal) * 100
        : 0

    const fdUsedPercent = kernelMetrics.fileDescriptorsMax > 0
        ? (kernelMetrics.fileDescriptors / kernelMetrics.fileDescriptorsMax) * 100
        : 0

    return (
        <div className="metrics-detail-panel">
            {/* CPU Metrics Section */}
            <div className="metric-category">
                <div className="category-header" onClick={() => toggleCategory('cpu')}>
                    <div className="category-title">
                        <Cpu size={20} />
                        <span>CPU Metrics</span>
                        <span className={`status-badge ${getHealthColor(cpuMetrics.usage)}`}>
                            {cpuMetrics.usage.toFixed(1)}%
                        </span>
                    </div>
                    {expandedCategory.cpu ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
                </div>

                {expandedCategory.cpu && (
                    <div className="category-content">
                        <div className="metrics-grid">
                            <MetricCard
                                label="User CPU"
                                value={`${cpuMetrics.user.toFixed(1)}%`}
                                icon={<Activity size={16} />}
                                health={getHealthColor(cpuMetrics.user)}
                            />
                            <MetricCard
                                label="System CPU"
                                value={`${cpuMetrics.system.toFixed(1)}%`}
                                icon={<Activity size={16} />}
                                health={getHealthColor(cpuMetrics.system)}
                            />
                            <MetricCard
                                label="I/O Wait"
                                value={`${cpuMetrics.iowait.toFixed(1)}%`}
                                icon={<Clock size={16} />}
                                health={getHealthColor(cpuMetrics.iowait * 2)}
                                subtitle="Time waiting for I/O"
                            />
                            <MetricCard
                                label="Idle"
                                value={`${cpuMetrics.idle.toFixed(1)}%`}
                                icon={<Activity size={16} />}
                                health="healthy"
                            />
                            <MetricCard
                                label="IRQ"
                                value={`${cpuMetrics.irq.toFixed(2)}%`}
                                icon={<Zap size={16} />}
                                subtitle="Hardware interrupts"
                            />
                            <MetricCard
                                label="Soft IRQ"
                                value={`${cpuMetrics.softirq.toFixed(2)}%`}
                                icon={<Zap size={16} />}
                                subtitle="Software interrupts"
                            />
                            <MetricCard
                                label="Context Switches"
                                value={formatNumber(cpuMetrics.contextSwitches)}
                                icon={<Activity size={16} />}
                                subtitle="Per second"
                            />
                            <MetricCard
                                label="Interrupts"
                                value={formatNumber(cpuMetrics.interrupts)}
                                icon={<Zap size={16} />}
                                subtitle="Per second"
                            />
                        </div>

                        {cpuMetrics.steal > 0 && (
                            <div className="metric-alert">
                                <AlertCircle size={16} />
                                <span>CPU Steal detected: {cpuMetrics.steal.toFixed(2)}% (VM overhead)</span>
                            </div>
                        )}

                        {/* Top CPU Processes (RCA) */}
                        {cpuMetrics.topProcesses.length > 0 && (
                            <div className="rca-section">
                                <h4>Top CPU Processes</h4>
                                <table className="rca-table">
                                    <thead>
                                        <tr>
                                            <th>PID</th>
                                            <th>Name</th>
                                            <th>Total %</th>
                                            <th>User %</th>
                                            <th>Sys %</th>
                                            <th>State</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {cpuMetrics.topProcesses.map(p => (
                                            <tr key={p.pid}>
                                                <td>{p.pid}</td>
                                                <td>{p.name}</td>
                                                <td className={getHealthColor(p.value)}>{p.value.toFixed(1)}%</td>
                                                <td>{p.user?.toFixed(1) || '-'}%</td>
                                                <td>{p.sys?.toFixed(1) || '-'}%</td>
                                                <td>{p.state || '-'}</td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}

                        {/* Blocked Processes (Wait Analysis) */}
                        {cpuMetrics.blockedProcesses.length > 0 && (
                            <div className="metric-alert warning">
                                <AlertCircle size={16} />
                                <span>Blocked Processes (Disk Sleep/Wait):</span>
                                <div className="tags-list">
                                    {cpuMetrics.blockedProcesses.map(p => (
                                        <span key={p.pid} className="process-tag">
                                            {p.name} ({p.pid})
                                            {p.wchan && p.wchan !== "0" && <span className="wchan">blocked on: {p.wchan}</span>}
                                        </span>
                                    ))}
                                </div>
                            </div>
                        )}
                    </div>
                )}
            </div>

            {/* Memory Metrics Section */}
            <div className="metric-category">
                <div className="category-header" onClick={() => toggleCategory('memory')}>
                    <div className="category-title">
                        <HardDrive size={20} />
                        <span>Memory Metrics</span>
                        <span className={`status-badge ${getHealthColor(memUsedPercent)}`}>
                            {memUsedPercent.toFixed(1)}%
                        </span>
                    </div>
                    {expandedCategory.memory ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
                </div>

                {expandedCategory.memory && (
                    <div className="category-content">
                        <div className="memory-breakdown">
                            <div className="breakdown-bar">
                                <div className="bar-segment used" style={{ width: `${memUsedPercent}%` }}>
                                    <span className="segment-label">Used</span>
                                </div>
                                <div className="bar-segment available" style={{ width: `${100 - memUsedPercent}%` }}>
                                    <span className="segment-label">Available</span>
                                </div>
                            </div>
                            <div className="breakdown-legend">
                                <span>Used: {formatBytes(memoryMetrics.total - memoryMetrics.available)}</span>
                                <span>Available: {formatBytes(memoryMetrics.available)}</span>
                                <span>Total: {formatBytes(memoryMetrics.total)}</span>
                            </div>
                        </div>

                        <div className="metrics-grid">
                            <MetricCard
                                label="Cached"
                                value={formatBytes(memoryMetrics.cached)}
                                icon={<Database size={16} />}
                                subtitle="Page cache"
                            />
                            <MetricCard
                                label="Buffers"
                                value={formatBytes(memoryMetrics.buffers)}
                                icon={<Database size={16} />}
                                subtitle="Buffer cache"
                            />
                            <MetricCard
                                label="Slab"
                                value={formatBytes(memoryMetrics.slab)}
                                icon={<Database size={16} />}
                                subtitle="Kernel objects"
                            />
                            <MetricCard
                                label="Dirty"
                                value={formatBytes(memoryMetrics.dirty)}
                                icon={<AlertCircle size={16} />}
                                subtitle="Waiting to write"
                            />
                            <MetricCard
                                label="Swap Used"
                                value={formatBytes(memoryMetrics.swapUsed)}
                                icon={<HardDrive size={16} />}
                                health={getHealthColor(swapUsedPercent)}
                                subtitle={`${swapUsedPercent.toFixed(1)}% of ${formatBytes(memoryMetrics.swapTotal)}`}
                            />
                            <MetricCard
                                label="Page Faults"
                                value={formatNumber(memoryMetrics.pageFaults)}
                                icon={<AlertCircle size={16} />}
                                subtitle="Minor faults/s"
                            />
                            <MetricCard
                                label="Major Faults"
                                value={formatNumber(memoryMetrics.majorPageFaults)}
                                icon={<AlertCircle size={16} />}
                                subtitle="Disk I/O required"
                            />
                        </div>

                        {/* Top Memory Processes (RCA) */}
                        {memoryMetrics.topProcesses.length > 0 && (
                            <div className="rca-section">
                                <h4>Top Memory Processes</h4>
                                <table className="rca-table">
                                    <thead>
                                        <tr>
                                            <th>PID</th>
                                            <th>Name</th>
                                            <th>RSS</th>
                                            <th>% Mem</th>
                                            <th>Swap</th>
                                            <th>OOM Score</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {memoryMetrics.topProcesses.map(p => (
                                            <tr key={p.pid}>
                                                <td>{p.pid}</td>
                                                <td>{p.name}</td>
                                                <td>{formatBytes(p.value)}</td>
                                                <td className={getHealthColor(p.percent)}>{p.percent.toFixed(1)}%</td>
                                                <td>{formatBytes(p.swap)}</td>
                                                <td>{p.oom}</td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}
                    </div>
                )}
            </div>

            {/* Disk I/O Metrics Section */}
            <div className="metric-category">
                <div className="category-header" onClick={() => toggleCategory('disk')}>
                    <div className="category-title">
                        <HardDrive size={20} />
                        <span>Disk I/O Metrics</span>
                        <span className="status-badge">{Object.keys(diskMetrics).length} devices</span>
                    </div>
                    {expandedCategory.disk ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
                </div>

                {expandedCategory.disk && (
                    <div className="category-content">
                        {Object.entries(diskMetrics).map(([device, stats]) => (
                            <div key={device} className="disk-detail">
                                <div className="disk-header">
                                    <span className="device-name">{device}</span>
                                    {stats.utilization > 0 && (
                                        <span className={`utilization-badge ${getHealthColor(stats.utilization)}`}>
                                            Util: {stats.utilization.toFixed(1)}%
                                        </span>
                                    )}
                                </div>
                                <div className="metrics-grid">
                                    <MetricCard
                                        label="Read Rate"
                                        value={formatBytes(stats.readBytes || 0) + '/s'}
                                        icon={<TrendingUp size={16} />}
                                        subtitle={`${formatNumber(stats.readOps || 0)} ops/s`}
                                    />
                                    <MetricCard
                                        label="Write Rate"
                                        value={formatBytes(stats.writeBytes || 0) + '/s'}
                                        icon={<TrendingUp size={16} />}
                                        subtitle={`${formatNumber(stats.writeOps || 0)} ops/s`}
                                    />
                                    <MetricCard
                                        label="Avg Read Latency"
                                        value={`${(stats.avgReadLatency || 0).toFixed(2)}ms`}
                                        icon={<Clock size={16} />}
                                        health={getHealthColor(stats.avgReadLatency > 20 ? 80 : 20)}
                                    />
                                    <MetricCard
                                        label="Avg Write Latency"
                                        value={`${(stats.avgWriteLatency || 0).toFixed(2)}ms`}
                                        icon={<Clock size={16} />}
                                        health={getHealthColor(stats.avgWriteLatency > 20 ? 80 : 20)}
                                    />
                                    <MetricCard
                                        label="I/O Time"
                                        value={`${(stats.ioTime || 0).toFixed(2)}ms`}
                                        icon={<Clock size={16} />}
                                        subtitle="Active I/O time"
                                    />
                                </div>
                            </div>
                        ))}

                        {/* Top I/O Processes (RCA) */}
                        {metrics.some(m => m.name.startsWith('rca_io_process')) && (
                            <div className="rca-section">
                                <h4>Top I/O Processes</h4>
                                <table className="rca-table">
                                    <thead>
                                        <tr>
                                            <th>PID</th>
                                            <th>Name</th>
                                            <th>Read/s</th>
                                            <th>Write/s</th>
                                            <th>Top Files</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {metrics
                                            .filter(m => m.name === 'rca_io_process_write_bytes_per_second')
                                            .sort((a, b) => b.value - a.value)
                                            .slice(0, 5)
                                            .map(m => {
                                                const readMetric = metrics.find(rm =>
                                                    rm.name === 'rca_io_process_read_bytes_per_second' &&
                                                    rm.labels.pid === m.labels.pid
                                                );

                                                // Find open files for this process
                                                const files = metrics.filter(fm =>
                                                    fm.name === 'rca_io_process_file_fd' &&
                                                    fm.labels.pid === m.labels.pid
                                                ).slice(0, 3).map(fm => fm.labels.path).filter(Boolean);

                                                return (
                                                    <tr key={m.labels.pid}>
                                                        <td>{m.labels.pid}</td>
                                                        <td>{m.labels.name}</td>
                                                        <td>{formatBytes(readMetric?.value || 0)}/s</td>
                                                        <td>{formatBytes(m.value)}/s</td>
                                                        <td className="small-text">{files.join(', ')}</td>
                                                    </tr>
                                                );
                                            })}
                                    </tbody>
                                </table>
                            </div>
                        )}
                    </div>
                )}
            </div>

            {/* Network Metrics Section */}
            <div className="metric-category">
                <div className="category-header" onClick={() => toggleCategory('network')}>
                    <div className="category-title">
                        <Network size={20} />
                        <span>Network Metrics</span>
                        <span className="status-badge">{Object.keys(networkMetrics).length} interfaces</span>
                    </div>
                    {expandedCategory.network ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
                </div>

                {expandedCategory.network && (
                    <div className="category-content">
                        {Object.entries(networkMetrics).map(([iface, stats]) => (
                            <div key={iface} className="network-detail">
                                <div className="network-header">
                                    <span className="interface-name">{iface}</span>
                                    {(stats.errIn > 0 || stats.errOut > 0) && (
                                        <span className="error-badge">
                                            <AlertCircle size={14} /> Errors detected
                                        </span>
                                    )}
                                </div>
                                <div className="metrics-grid">
                                    <MetricCard
                                        label="RX Rate"
                                        value={formatBytes(stats.bytesRecv || 0) + '/s'}
                                        icon={<TrendingUp size={16} />}
                                        subtitle={`${formatNumber(stats.packetsRecv || 0)} pkt/s`}
                                    />
                                    <MetricCard
                                        label="TX Rate"
                                        value={formatBytes(stats.bytesSent || 0) + '/s'}
                                        icon={<TrendingUp size={16} />}
                                        subtitle={`${formatNumber(stats.packetsSent || 0)} pkt/s`}
                                    />
                                    {(stats.errIn > 0 || stats.errOut > 0) && (
                                        <MetricCard
                                            label="Errors"
                                            value={`${formatNumber((stats.errIn || 0) + (stats.errOut || 0))}`}
                                            icon={<AlertCircle size={16} />}
                                            health="warning"
                                            subtitle={`RX: ${formatNumber(stats.errIn || 0)}, TX: ${formatNumber(stats.errOut || 0)}`}
                                        />
                                    )}
                                    {(stats.dropIn > 0 || stats.dropOut > 0) && (
                                        <MetricCard
                                            label="Drops"
                                            value={`${formatNumber((stats.dropIn || 0) + (stats.dropOut || 0))}`}
                                            icon={<AlertCircle size={16} />}
                                            health="warning"
                                            subtitle={`RX: ${formatNumber(stats.dropIn || 0)}, TX: ${formatNumber(stats.dropOut || 0)}`}
                                        />
                                    )}
                                </div>
                            </div>
                        ))}

                        {/* Top Network Processes (RCA) */}
                        {metrics.some(m => m.name.startsWith('rca_net_process')) && (
                            <div className="rca-section">
                                <h4>Top Network Processes</h4>
                                <table className="rca-table">
                                    <thead>
                                        <tr>
                                            <th>PID</th>
                                            <th>Name</th>
                                            <th>Conns</th>
                                            <th>Queue Bytes</th>
                                            <th>State Breakdown</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {metrics
                                            .filter(m => m.name === 'rca_net_process_connections')
                                            .sort((a, b) => b.value - a.value)
                                            .slice(0, 5)
                                            .map(m => {
                                                const queue = metrics.find(qm =>
                                                    qm.name === 'rca_net_process_queued_bytes' &&
                                                    qm.labels.pid === m.labels.pid
                                                );

                                                // Get state breakdown
                                                const states = metrics
                                                    .filter(sm => sm.name === 'rca_net_process_connections_by_state' && sm.labels.pid === m.labels.pid)
                                                    .map(sm => `${sm.labels.state}: ${sm.value}`)
                                                    .join(', ');

                                                return (
                                                    <tr key={m.labels.pid}>
                                                        <td>{m.labels.pid}</td>
                                                        <td>{m.labels.name}</td>
                                                        <td>{m.value}</td>
                                                        <td>{formatBytes(queue?.value || 0)}</td>
                                                        <td className="small-text">{states}</td>
                                                    </tr>
                                                );
                                            })}
                                    </tbody>
                                </table>
                            </div>
                        )}
                    </div>
                )}
            </div>

            {/* Kernel Metrics Section */}
            <div className="metric-category">
                <div className="category-header" onClick={() => toggleCategory('kernel')}>
                    <div className="category-title">
                        <Activity size={20} />
                        <span>Kernel & System Metrics</span>
                        <span className={`status-badge ${getHealthColor(fdUsedPercent)}`}>
                            FD: {fdUsedPercent.toFixed(0)}%
                        </span>
                    </div>
                    {expandedCategory.kernel ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
                </div>

                {expandedCategory.kernel && (
                    <div className="category-content">
                        <div className="metrics-grid">
                            <MetricCard
                                label="Processes Running"
                                value={formatNumber(kernelMetrics.processesRunning)}
                                icon={<Activity size={16} />}
                            />
                            <MetricCard
                                label="Processes Blocked"
                                value={formatNumber(kernelMetrics.processesBlocked)}
                                icon={<AlertCircle size={16} />}
                                health={kernelMetrics.processesBlocked > 5 ? 'warning' : 'healthy'}
                                subtitle="Waiting for I/O"
                            />
                            <MetricCard
                                label="Total Threads"
                                value={formatNumber(kernelMetrics.threadsTotal)}
                                icon={<Activity size={16} />}
                            />
                            <MetricCard
                                label="Total Forks"
                                value={formatNumber(kernelMetrics.forksTotal)}
                                icon={<Activity size={16} />}
                                subtitle="Since boot"
                            />
                            <MetricCard
                                label="File Descriptors"
                                value={formatNumber(kernelMetrics.fileDescriptors)}
                                icon={<Database size={16} />}
                                health={getHealthColor(fdUsedPercent)}
                                subtitle={`${fdUsedPercent.toFixed(1)}% of ${formatNumber(kernelMetrics.fileDescriptorsMax)}`}
                            />
                            <MetricCard
                                label="Inodes Allocated"
                                value={formatNumber(kernelMetrics.inodes)}
                                icon={<Database size={16} />}
                            />
                        </div>
                    </div>
                )}
            </div>
        </div>
    )
}

function MetricCard({ label, value, icon, health = 'normal', subtitle }) {
    return (
        <div className={`metric-card ${health}`}>
            <div className="metric-icon">{icon}</div>
            <div className="metric-content">
                <div className="metric-label">{label}</div>
                <div className="metric-value">{value}</div>
                {subtitle && <div className="metric-subtitle">{subtitle}</div>}
            </div>
        </div>
    )
}

export default MetricsDetailPanel

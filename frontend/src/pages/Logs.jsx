import { useState, useEffect, useCallback, useRef } from 'react'
import { Search, Filter, Clock, Activity, AlertTriangle, Info, Terminal, RefreshCw, Server, X } from 'lucide-react'
import './Logs.css'

const API_BASE = '/api/v1'

const LOG_LEVEL_COLORS = {
    emerg: '#ef4444',
    alert: '#ef4444',
    crit: '#ef4444',
    err: '#ef4444',
    warn: '#f59e0b',
    notice: '#3b82f6',
    info: '#3b82f6',
    debug: '#6b7280',
    unknown: '#6b7280',
}

function Logs() {
    const [query, setQuery] = useState('')
    const [timeRange, setTimeRange] = useState('1h')
    const [results, setResults] = useState(null)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState(null)
    const [autoRefresh, setAutoRefresh] = useState(false)

    // Filters
    const [levels, setLevels] = useState([])
    const [sources, setSources] = useState([])

    const searchInputRef = useRef(null)

    const parseTimeWindow = (window) => {
        return window
    }

    const performSearch = useCallback(async () => {
        setLoading(true)
        setError(null)
        try {
            const params = new URLSearchParams()
            if (query) params.append('q', query)
            if (timeRange !== 'custom') {
                params.append('window', parseTimeWindow(timeRange))
            }
            if (levels.length > 0) {
                params.append('level', levels.join(','))
            }

            const res = await fetch(`${API_BASE}/logs/search?${params.toString()}`)
            if (!res.ok) {
                throw new Error('Failed to fetch logs')
            }
            const data = await res.json()
            setResults(data)
        } catch (err) {
            setError(err.message)
        } finally {
            setLoading(false)
        }
    }, [query, timeRange, levels, sources])

    useEffect(() => {
        performSearch()
    }, [timeRange, levels])

    useEffect(() => {
        let interval;
        if (autoRefresh) {
            interval = setInterval(performSearch, 5000)
        }
        return () => clearInterval(interval)
    }, [autoRefresh, performSearch])

    const handleSearch = (e) => {
        e.preventDefault()
        performSearch()
    }

    const renderTimeline = () => {
        if (!results || !results.timeline || results.timeline.length === 0) return null

        const maxCount = Math.max(...results.timeline.map(b => b.count))
        if (maxCount === 0) return null

        return (
            <div className="logs-timeline">
                {results.timeline.map((bucket, idx) => {
                    const height = Math.max(2, (bucket.count / maxCount) * 100)
                    return (
                        <div
                            key={idx}
                            className="timeline-bar"
                            style={{ height: `${height}%` }}
                            title={`${new Date(bucket.start).toLocaleTimeString()}: ${bucket.count} events`}
                        />
                    )
                })}
            </div>
        )
    }

    const formatDate = (dateString) => {
        const d = new Date(dateString)
        return d.toISOString().replace('T', ' ').substring(0, 23)
    }

    return (
        <div className="logs-page">
            <div className="logs-toolbar">
                <form onSubmit={handleSearch} className="search-form">
                    <div className="search-input-wrapper">
                        <Search className="search-icon" size={18} />
                        <input
                            ref={searchInputRef}
                            type="text"
                            value={query}
                            onChange={e => setQuery(e.target.value)}
                            placeholder="Search logs (e.g., error AND service:api)"
                            className="logs-search-input"
                        />
                    </div>
                    <button type="submit" className="btn-primary" disabled={loading}>
                        Search
                    </button>
                </form>

                <div className="toolbar-controls">
                    <select
                        value={timeRange}
                        onChange={e => setTimeRange(e.target.value)}
                        className="time-select"
                    >
                        <option value="15m">Last 15 minutes</option>
                        <option value="1h">Last 1 hour</option>
                        <option value="6h">Last 6 hours</option>
                        <option value="24h">Last 24 hours</option>
                        <option value="7d">Last 7 days</option>
                    </select>

                    <button
                        className={`btn-icon ${autoRefresh ? 'active' : ''}`}
                        onClick={() => setAutoRefresh(!autoRefresh)}
                        title="Auto-refresh (5s)"
                    >
                        <RefreshCw size={18} className={loading ? 'spin' : ''} />
                    </button>
                </div>
            </div>

            {error && (
                <div className="logs-error">
                    <AlertTriangle size={16} />
                    <span>{error}</span>
                </div>
            )}

            <div className="logs-content">
                <div className="logs-sidebar">
                    <div className="sidebar-section">
                        <div className="sidebar-header">
                            <Filter size={16} />
                            <h3>Severity</h3>
                        </div>
                        <div className="facet-list">
                            {results?.level_counts?.map(bucket => (
                                <div key={bucket.key} className="facet-item">
                                    <span
                                        className="facet-color"
                                        style={{ background: LOG_LEVEL_COLORS[bucket.key] || LOG_LEVEL_COLORS.unknown }}
                                    />
                                    <span className="facet-label">{bucket.key}</span>
                                    <span className="facet-count">{bucket.count.toLocaleString()}</span>
                                </div>
                            ))}
                        </div>
                    </div>

                    <div className="sidebar-section">
                        <div className="sidebar-header">
                            <Server size={16} />
                            <h3>Service</h3>
                        </div>
                        <div className="facet-list">
                            {results?.service_counts?.map(bucket => (
                                <div key={bucket.key} className="facet-item">
                                    <span className="facet-label" title={bucket.key}>{bucket.key || 'unknown'}</span>
                                    <span className="facet-count">{bucket.count.toLocaleString()}</span>
                                </div>
                            ))}
                        </div>
                    </div>

                    <div className="sidebar-section">
                        <div className="sidebar-header">
                            <Terminal size={16} />
                            <h3>Source / Probe</h3>
                        </div>
                        <div className="facet-list">
                            {results?.collector_counts?.map(bucket => (
                                <div key={bucket.key} className="facet-item">
                                    <span className="facet-label" title={bucket.key}>{bucket.key}</span>
                                    <span className="facet-count">{bucket.count.toLocaleString()}</span>
                                </div>
                            ))}
                        </div>
                    </div>
                </div>

                <div className="logs-main">
                    {renderTimeline()}

                    <div className="logs-results-header">
                        {results && !loading && (
                            <span className="results-count">
                                {results.total.toLocaleString()} hits in {results.returned} results
                            </span>
                        )}
                        {loading && <span className="results-count">Searching...</span>}
                    </div>

                    <div className="logs-table-container">
                        <table className="logs-table">
                            <thead>
                                <tr>
                                    <th className="col-time">Time</th>
                                    <th className="col-level">Level</th>
                                    <th className="col-service">Service</th>
                                    <th className="col-message">Message</th>
                                </tr>
                            </thead>
                            <tbody>
                                {results?.entries?.map((entry, idx) => (
                                    <tr key={entry.id || idx} className={`log-row level-${entry.level}`}>
                                        <td className="col-time">{formatDate(entry.timestamp)}</td>
                                        <td className="col-level">
                                            <span
                                                className="level-badge"
                                                style={{ color: LOG_LEVEL_COLORS[entry.level] || LOG_LEVEL_COLORS.unknown }}
                                            >
                                                {entry.level}
                                            </span>
                                        </td>
                                        <td className="col-service" title={entry.service || entry.hostname}>{entry.service || entry.hostname}</td>
                                        <td className="col-message">
                                            <div className="message-content">{entry.message}</div>
                                            {entry.labels && Object.keys(entry.labels).length > 0 && (
                                                <div className="log-labels">
                                                    {Object.entries(entry.labels).map(([k, v]) => (
                                                        <span key={k} className="log-label">{k}={v}</span>
                                                    ))}
                                                </div>
                                            )}
                                        </td>
                                    </tr>
                                ))}
                                {results?.entries?.length === 0 && (
                                    <tr>
                                        <td colSpan="4" className="empty-results">
                                            No logs found matching your query.
                                        </td>
                                    </tr>
                                )}
                            </tbody>
                        </table>
                    </div>
                </div>
            </div>
        </div>
    )
}

export default Logs

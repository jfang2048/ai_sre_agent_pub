import { useState, useEffect } from 'react'
import { Plus, AlertTriangle, Clock, User, CheckCircle, XCircle, Search, Filter } from 'lucide-react'
import './Incidents.css'

const API_BASE = '/api/v1'

function Incidents() {
  const [incidents, setIncidents] = useState([])
  const [filteredIncidents, setFilteredIncidents] = useState([])
  const [loading, setLoading] = useState(true)
  const [showCreateModal, setShowCreateModal] = useState(false)
  const [selectedIncident, setSelectedIncident] = useState(null)
  const [filterSeverity, setFilterSeverity] = useState('all')
  const [filterState, setFilterState] = useState('all')
  const [searchQuery, setSearchQuery] = useState('')

  const [newIncident, setNewIncident] = useState({
    title: '',
    description: '',
    severity: 'P2',
    state: 'detected'
  })

  useEffect(() => {
    fetchIncidents()
    const interval = setInterval(fetchIncidents, 5000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    filterIncidents()
  }, [incidents, filterSeverity, filterState, searchQuery])

  const fetchIncidents = async () => {
    try {
      const res = await fetch(`${API_BASE}/incidents`)
      if (res.ok) {
        const data = await res.json()
        setIncidents(data || [])
        setLoading(false)
      }
    } catch (err) {
      console.error('Failed to fetch incidents:', err)
    }
  }

  const filterIncidents = () => {
    let filtered = [...incidents]

    if (filterSeverity !== 'all') {
      filtered = filtered.filter(i => i.severity === filterSeverity)
    }

    if (filterState !== 'all') {
      filtered = filtered.filter(i => i.state === filterState)
    }

    if (searchQuery) {
      const query = searchQuery.toLowerCase()
      filtered = filtered.filter(i =>
        i.title?.toLowerCase().includes(query) ||
        i.description?.toLowerCase().includes(query) ||
        i.id?.toLowerCase().includes(query)
      )
    }

    filtered.sort((a, b) => new Date(b.detected_at) - new Date(a.detected_at))
    setFilteredIncidents(filtered)
  }

  const createIncident = async (e) => {
    e.preventDefault()
    try {
      const res = await fetch(`${API_BASE}/incidents`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newIncident)
      })

      if (res.ok) {
        setShowCreateModal(false)
        setNewIncident({ title: '', description: '', severity: 'P2', state: 'detected' })
        fetchIncidents()
      }
    } catch (err) {
      console.error('Failed to create incident:', err)
    }
  }

  const updateIncident = async (incident) => {
    try {
      const res = await fetch(`${API_BASE}/incidents/${incident.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(incident)
      })

      if (res.ok) {
        fetchIncidents()
      }
    } catch (err) {
      console.error('Failed to update incident:', err)
    }
  }

  const resolveIncident = async (id, resolution) => {
    try {
      const res = await fetch(`${API_BASE}/incidents/${id}?action=resolve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ resolution })
      })

      if (res.ok) {
        fetchIncidents()
        setSelectedIncident(null)
      }
    } catch (err) {
      console.error('Failed to resolve incident:', err)
    }
  }

  const assignCommander = async (id, commander) => {
    try {
      const res = await fetch(`${API_BASE}/incidents/${id}?action=assign`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ commander })
      })

      if (res.ok) {
        fetchIncidents()
      }
    } catch (err) {
      console.error('Failed to assign commander:', err)
    }
  }

  const getSeverityColor = (severity) => {
    switch (severity) {
      case 'P0': return '#ef4444'
      case 'P1': return '#f59e0b'
      case 'P2': return '#6366f1'
      case 'P3': return '#6b7280'
      default: return '#6b7280'
    }
  }

  const getStateColor = (state) => {
    switch (state) {
      case 'detected': return '#ef4444'
      case 'acknowledged': return '#f59e0b'
      case 'investigating': return '#6366f1'
      case 'mitigating': return '#8b5cf6'
      case 'resolved': return '#10b981'
      case 'closed': return '#6b7280'
      default: return '#6b7280'
    }
  }

  const formatDuration = (nanos) => {
    if (!nanos) return '-'
    const seconds = nanos / 1e9
    const minutes = Math.floor(seconds / 60)
    const hours = Math.floor(minutes / 60)
    const days = Math.floor(hours / 24)

    if (days > 0) return `${days}d ${hours % 24}h`
    if (hours > 0) return `${hours}h ${minutes % 60}m`
    if (minutes > 0) return `${minutes}m`
    return `${Math.floor(seconds)}s`
  }

  const stats = {
    total: incidents.length,
    p0: incidents.filter(i => i.severity === 'P0' && i.state !== 'resolved' && i.state !== 'closed').length,
    p1: incidents.filter(i => i.severity === 'P1' && i.state !== 'resolved' && i.state !== 'closed').length,
    active: incidents.filter(i => i.state !== 'resolved' && i.state !== 'closed').length,
    resolved: incidents.filter(i => i.state === 'resolved' || i.state === 'closed').length
  }

  return (
    <div className="incidents-page">
      {/* Stats Header */}
      <div className="incidents-stats">
        <div className="stat-item">
          <div className="stat-label">Total Incidents</div>
          <div className="stat-value">{stats.total}</div>
        </div>
        <div className="stat-item critical">
          <div className="stat-label">P0 (Critical)</div>
          <div className="stat-value">{stats.p0}</div>
        </div>
        <div className="stat-item warning">
          <div className="stat-label">P1 (High)</div>
          <div className="stat-value">{stats.p1}</div>
        </div>
        <div className="stat-item">
          <div className="stat-label">Active</div>
          <div className="stat-value">{stats.active}</div>
        </div>
        <div className="stat-item resolved">
          <div className="stat-label">Resolved</div>
          <div className="stat-value">{stats.resolved}</div>
        </div>
      </div>

      {/* Actions Bar */}
      <div className="actions-bar">
        <div className="search-filters">
          <div className="search-box">
            <Search size={16} />
            <input
              type="text"
              placeholder="Search incidents..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
            />
          </div>

          <select
            value={filterSeverity}
            onChange={(e) => setFilterSeverity(e.target.value)}
            className="filter-select"
          >
            <option value="all">All Severities</option>
            <option value="P0">P0 - Critical</option>
            <option value="P1">P1 - High</option>
            <option value="P2">P2 - Medium</option>
            <option value="P3">P3 - Low</option>
          </select>

          <select
            value={filterState}
            onChange={(e) => setFilterState(e.target.value)}
            className="filter-select"
          >
            <option value="all">All States</option>
            <option value="detected">Detected</option>
            <option value="acknowledged">Acknowledged</option>
            <option value="investigating">Investigating</option>
            <option value="mitigating">Mitigating</option>
            <option value="resolved">Resolved</option>
            <option value="closed">Closed</option>
          </select>
        </div>

        <button className="btn-primary" onClick={() => setShowCreateModal(true)}>
          <Plus size={16} />
          Create Incident
        </button>
      </div>

      {/* Incidents List */}
      {loading ? (
        <div className="loading-state">Loading incidents...</div>
      ) : filteredIncidents.length === 0 ? (
        <div className="empty-state">
          <AlertTriangle size={48} />
          <h3>No incidents found</h3>
          <p>Create a new incident to get started.</p>
        </div>
      ) : (
        <div className="incidents-list">
          {filteredIncidents.map(incident => (
            <div
              key={incident.id}
              className="incident-card"
              onClick={() => setSelectedIncident(incident)}
            >
              <div className="incident-header">
                <div className="incident-severity" style={{ background: getSeverityColor(incident.severity) }}>
                  {incident.severity}
                </div>
                <div className="incident-state" style={{ color: getStateColor(incident.state) }}>
                  {incident.state}
                </div>
                <div className="incident-time">
                  <Clock size={14} />
                  {new Date(incident.detected_at).toLocaleString()}
                </div>
              </div>

              <h4 className="incident-title">{incident.title}</h4>
              <p className="incident-description">{incident.description}</p>

              <div className="incident-footer">
                <div className="incident-metrics">
                  {incident.mttr && (
                    <span className="metric-badge">MTTR: {formatDuration(incident.mttr)}</span>
                  )}
                  {incident.mtta && (
                    <span className="metric-badge">MTTA: {formatDuration(incident.mtta)}</span>
                  )}
                  {incident.incident_commander && (
                    <span className="commander-badge">
                      <User size={12} />
                      {incident.incident_commander}
                    </span>
                  )}
                </div>
                {incident.state === 'resolved' || incident.state === 'closed' ? (
                  <CheckCircle size={18} className="status-icon resolved" />
                ) : (
                  <AlertTriangle size={18} className="status-icon active" />
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Create Incident Modal */}
      {showCreateModal && (
        <div className="modal-overlay" onClick={() => setShowCreateModal(false)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Create New Incident</h3>
              <button onClick={() => setShowCreateModal(false)} className="close-btn">×</button>
            </div>
            <form onSubmit={createIncident} className="modal-body">
              <div className="form-group">
                <label>Title</label>
                <input
                  type="text"
                  required
                  value={newIncident.title}
                  onChange={e => setNewIncident({ ...newIncident, title: e.target.value })}
                  placeholder="Brief description of the incident"
                />
              </div>

              <div className="form-group">
                <label>Description</label>
                <textarea
                  value={newIncident.description}
                  onChange={e => setNewIncident({ ...newIncident, description: e.target.value })}
                  placeholder="Detailed description of the incident"
                  rows={4}
                />
              </div>

              <div className="form-row">
                <div className="form-group">
                  <label>Severity</label>
                  <select
                    value={newIncident.severity}
                    onChange={e => setNewIncident({ ...newIncident, severity: e.target.value })}
                  >
                    <option value="P0">P0 - Critical (service down)</option>
                    <option value="P1">P1 - High (significant degradation)</option>
                    <option value="P2">P2 - Medium (minor impact)</option>
                    <option value="P3">P3 - Low (no service impact)</option>
                  </select>
                </div>

                <div className="form-group">
                  <label>Initial State</label>
                  <select
                    value={newIncident.state}
                    onChange={e => setNewIncident({ ...newIncident, state: e.target.value })}
                  >
                    <option value="detected">Detected</option>
                    <option value="acknowledged">Acknowledged</option>
                  </select>
                </div>
              </div>

              <div className="modal-footer">
                <button type="button" onClick={() => setShowCreateModal(false)} className="btn-secondary">
                  Cancel
                </button>
                <button type="submit" className="btn-primary">
                  Create Incident
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Incident Detail Modal */}
      {selectedIncident && (
        <div className="modal-overlay" onClick={() => setSelectedIncident(null)}>
          <div className="modal large" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Incident Details</h3>
              <button onClick={() => setSelectedIncident(null)} className="close-btn">×</button>
            </div>
            <div className="modal-body incident-detail">
              <div className="detail-header">
                <div className="detail-severity" style={{ background: getSeverityColor(selectedIncident.severity) }}>
                  {selectedIncident.severity}
                </div>
                <div className="detail-state" style={{ color: getStateColor(selectedIncident.state) }}>
                  {selectedIncident.state}
                </div>
                <span className="detail-id">{selectedIncident.id}</span>
              </div>

              <h2>{selectedIncident.title}</h2>
              <p className="detail-description">{selectedIncident.description}</p>

              <div className="detail-timeline">
                <h4>Timeline</h4>
                <div className="timeline-item">
                  <span className="timeline-label">Detected</span>
                  <span className="timeline-value">{new Date(selectedIncident.detected_at).toLocaleString()}</span>
                </div>
                {selectedIncident.acknowledged_at && (
                  <div className="timeline-item">
                    <span className="timeline-label">Acknowledged</span>
                    <span className="timeline-value">{new Date(selectedIncident.acknowledged_at).toLocaleString()}</span>
                  </div>
                )}
                {selectedIncident.resolved_at && (
                  <div className="timeline-item">
                    <span className="timeline-label">Resolved</span>
                    <span className="timeline-value">{new Date(selectedIncident.resolved_at).toLocaleString()}</span>
                  </div>
                )}
              </div>

              <div className="detail-metrics">
                <h4>Metrics</h4>
                <div className="metric-grid">
                  {selectedIncident.mttd && (
                    <div className="metric-item">
                      <span className="metric-label">MTTD</span>
                      <span className="metric-value">{formatDuration(selectedIncident.mttd)}</span>
                    </div>
                  )}
                  {selectedIncident.mtta && (
                    <div className="metric-item">
                      <span className="metric-label">MTTA</span>
                      <span className="metric-value">{formatDuration(selectedIncident.mtta)}</span>
                    </div>
                  )}
                  {selectedIncident.mttr && (
                    <div className="metric-item">
                      <span className="metric-label">MTTR</span>
                      <span className="metric-value">{formatDuration(selectedIncident.mttr)}</span>
                    </div>
                  )}
                  {selectedIncident.error_budget_impact && (
                    <div className="metric-item">
                      <span className="metric-label">Error Budget Impact</span>
                      <span className="metric-value">{selectedIncident.error_budget_impact}%</span>
                    </div>
                  )}
                </div>
              </div>

              {selectedIncident.root_cause && (
                <div className="detail-section">
                  <h4>Root Cause</h4>
                  <p>{selectedIncident.root_cause}</p>
                </div>
              )}

              {selectedIncident.resolution && (
                <div className="detail-section">
                  <h4>Resolution</h4>
                  <p>{selectedIncident.resolution}</p>
                </div>
              )}

              {(selectedIncident.state !== 'resolved' && selectedIncident.state !== 'closed') && (
                <div className="detail-actions">
                  <div className="form-group">
                    <label>Assign Commander</label>
                    <div className="input-group">
                      <input
                        type="text"
                        placeholder="Enter commander name"
                        onKeyPress={e => {
                          if (e.key === 'Enter') {
                            assignCommander(selectedIncident.id, e.target.value)
                            e.target.value = ''
                          }
                        }}
                      />
                      <button
                        onClick={(e) => {
                          const input = e.target.previousElementSibling
                          if (input.value) {
                            assignCommander(selectedIncident.id, input.value)
                            input.value = ''
                          }
                        }}
                        className="btn-secondary"
                      >
                        Assign
                      </button>
                    </div>
                  </div>

                  <div className="form-group">
                    <label>Resolve Incident</label>
                    <div className="input-group">
                      <input
                        type="text"
                        placeholder="Enter resolution details"
                        ref={el => {
                          if (el) el.resolutionInput = el
                        }}
                      />
                      <button
                        onClick={(e) => {
                          const input = e.target.previousElementSibling
                          if (input.value) {
                            resolveIncident(selectedIncident.id, input.value)
                            input.value = ''
                            setSelectedIncident(null)
                          }
                        }}
                        className="btn-primary"
                      >
                        Resolve
                      </button>
                    </div>
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default Incidents

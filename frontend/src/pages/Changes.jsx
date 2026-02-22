import { useState, useEffect } from 'react'
import { Plus, GitBranch, Clock, User, CheckCircle, XCircle, AlertCircle, Play, SkipForward, RotateCcw } from 'lucide-react'
import './Changes.css'

const API_BASE = '/api/v1'

function Changes() {
  const [changes, setChanges] = useState([])
  const [loading, setLoading] = useState(true)
  const [showCreateModal, setShowCreateModal] = useState(false)
  const [selectedChange, setSelectedChange] = useState(null)
  const [filterStatus, setFilterStatus] = useState('all')

  const [newChange, setNewChange] = useState({
    title: '',
    description: '',
    tier: '3',
    change_type: 'deploy',
    risk_level: 'medium',
    rollback_plan: ''
  })

  useEffect(() => {
    fetchChanges()
    const interval = setInterval(fetchChanges, 5000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    filterChanges()
  }, [changes, filterStatus])

  const fetchChanges = async () => {
    try {
      const res = await fetch(`${API_BASE}/changes`)
      if (res.ok) {
        const data = await res.json()
        setChanges(data || [])
        setLoading(false)
      }
    } catch (err) {
      console.error('Failed to fetch changes:', err)
    }
  }

  const filterChanges = () => {
    // Client-side filtering is done when rendering
  }

  const createChange = async (e) => {
    e.preventDefault()
    try {
      const payload = {
        ...newChange,
        tier: parseInt(newChange.tier),
        planned_start: new Date().toISOString(),
        planned_end: new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString(), // 2 hours from now
        canary_config: {
          initial_percentage: 5,
          increment_step: 5,
          increment_interval: '5m',
          max_percentage: 100,
          success_criteria: {
            max_error_rate: 1,
            max_latency_increase: 20,
            min_availability: 99.9
          }
        }
      }

      const res = await fetch(`${API_BASE}/changes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })

      if (res.ok) {
        setShowCreateModal(false)
        setNewChange({
          title: '',
          description: '',
          tier: '3',
          change_type: 'deploy',
          risk_level: 'medium',
          rollback_plan: ''
        })
        fetchChanges()
      }
    } catch (err) {
      console.error('Failed to create change:', err)
    }
  }

  const approveChange = async (id) => {
    try {
      const approver = prompt('Enter your name to approve this change:')
      if (!approver) return

      const res = await fetch(`${API_BASE}/changes/${id}?action=approve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ approver })
      })

      if (res.ok) {
        fetchChanges()
        if (selectedChange?.id === id) {
          setSelectedChange(null)
        }
      }
    } catch (err) {
      console.error('Failed to approve change:', err)
    }
  }

  const startChange = async (id) => {
    try {
      const res = await fetch(`${API_BASE}/changes/${id}?action=start`, {
        method: 'POST'
      })

      if (res.ok) {
        fetchChanges()
        if (selectedChange?.id === id) {
          const updated = await fetch(`${API_BASE}/changes/${id}`).then(r => r.json())
          setSelectedChange(updated)
        }
      }
    } catch (err) {
      console.error('Failed to start change:', err)
    }
  }

  const completeChange = async (id) => {
    try {
      const res = await fetch(`${API_BASE}/changes/${id}?action=complete`, {
        method: 'POST'
      })

      if (res.ok) {
        fetchChanges()
        if (selectedChange?.id === id) {
          setSelectedChange(null)
        }
      }
    } catch (err) {
      console.error('Failed to complete change:', err)
    }
  }

  const rollbackChange = async (id) => {
    try {
      const reason = prompt('Enter the reason for rollback:')
      if (!reason) return

      const res = await fetch(`${API_BASE}/changes/${id}?action=rollback`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason })
      })

      if (res.ok) {
        fetchChanges()
        if (selectedChange?.id === id) {
          const updated = await fetch(`${API_BASE}/changes/${id}`).then(r => r.json())
          setSelectedChange(updated)
        }
      }
    } catch (err) {
      console.error('Failed to rollback change:', err)
    }
  }

  const getStatusColor = (status) => {
    switch (status) {
      case 'completed': return '#10b981'
      case 'in_progress': return '#6366f1'
      case 'rolled_back': return '#ef4444'
      case 'failed': return '#ef4444'
      case 'cancelled': return '#6b7280'
      default: return '#888'
    }
  }

  const getStatusIcon = (status) => {
    switch (status) {
      case 'completed': return CheckCircle
      case 'in_progress': return Play
      case 'rolled_back': return RotateCcw
      case 'failed': return XCircle
      case 'cancelled': return XCircle
      default: return Clock
    }
  }

  const getRiskColor = (risk) => {
    switch (risk) {
      case 'high': return '#ef4444'
      case 'medium': return '#f59e0b'
      case 'low': return '#10b981'
      default: return '#6b7280'
    }
  }

  const getApprovalStatusColor = (status) => {
    switch (status) {
      case 'approved': return '#10b981'
      case 'pending': return '#f59e0b'
      case 'rejected': return '#ef4444'
      case 'no_approval_required': return '#6b7280'
      default: return '#6b7280'
    }
  }

  const filteredChanges = filterStatus === 'all'
    ? changes
    : changes.filter(c => c.status === filterStatus)

  const stats = {
    total: changes.length,
    planned: changes.filter(c => c.status === 'planned').length,
    inProgress: changes.filter(c => c.status === 'in_progress').length,
    completed: changes.filter(c => c.status === 'completed').length,
    rolledBack: changes.filter(c => c.status === 'rolled_back').length
  }

  return (
    <div className="changes-page">
      {/* Stats Header */}
      <div className="changes-stats">
        <div className="stat-item">
          <div className="stat-label">Total Changes</div>
          <div className="stat-value">{stats.total}</div>
        </div>
        <div className="stat-item">
          <div className="stat-label">Planned</div>
          <div className="stat-value">{stats.planned}</div>
        </div>
        <div className="stat-item active">
          <div className="stat-label">In Progress</div>
          <div className="stat-value">{stats.inProgress}</div>
        </div>
        <div className="stat-item resolved">
          <div className="stat-label">Completed</div>
          <div className="stat-value">{stats.completed}</div>
        </div>
        <div className="stat-item error">
          <div className="stat-label">Rolled Back</div>
          <div className="stat-value">{stats.rolledBack}</div>
        </div>
      </div>

      {/* Actions Bar */}
      <div className="actions-bar">
        <div className="filter-group">
          <label>Status:</label>
          <select
            value={filterStatus}
            onChange={(e) => setFilterStatus(e.target.value)}
            className="filter-select"
          >
            <option value="all">All Statuses</option>
            <option value="planned">Planned</option>
            <option value="in_progress">In Progress</option>
            <option value="completed">Completed</option>
            <option value="rolled_back">Rolled Back</option>
            <option value="failed">Failed</option>
          </select>
        </div>

        <button className="btn-primary" onClick={() => setShowCreateModal(true)}>
          <Plus size={16} />
          Create Change
        </button>
      </div>

      {/* Changes List */}
      {loading ? (
        <div className="loading-state">Loading changes...</div>
      ) : filteredChanges.length === 0 ? (
        <div className="empty-state">
          <GitBranch size={48} />
          <h3>No changes found</h3>
          <p>Create a new change request to get started.</p>
        </div>
      ) : (
        <div className="changes-list">
          {filteredChanges.map(change => {
            const StatusIcon = getStatusIcon(change.status)
            return (
              <div
                key={change.id}
                className="change-card"
                onClick={() => setSelectedChange(change)}
              >
                <div className="change-header">
                  <div className="change-status" style={{ color: getStatusColor(change.status) }}>
                    <StatusIcon size={16} />
                    {change.status.replace('_', ' ')}
                  </div>
                  <div className="change-tier">Tier {change.tier}</div>
                  <div className="change-type">{change.change_type}</div>
                  <div
                    className="change-risk"
                    style={{ background: getRiskColor(change.risk_level) }}
                  >
                    {change.risk_level}
                  </div>
                </div>

                <h4 className="change-title">{change.title}</h4>
                <p className="change-description">{change.description}</p>

                <div className="change-footer">
                  <div className="change-approval">
                    <span
                      className="approval-badge"
                      style={{ background: getApprovalStatusColor(change.approval_status) }}
                    >
                      {change.approval_status.replace('_', ' ')}
                    </span>
                    {change.approved_by && (
                      <span className="approver">by {change.approved_by}</span>
                    )}
                  </div>
                  <div className="change-time">
                    <Clock size={14} />
                    {new Date(change.created_at).toLocaleString()}
                  </div>
                </div>

                {/* Canary Progress */}
                {change.canary_status && change.status === 'in_progress' && (
                  <div className="canary-progress">
                    <div className="canary-header">
                      <span>Canary Deployment</span>
                      <span className="canary-percent">{change.canary_status.current_percentage}%</span>
                    </div>
                    <div className="progress-bar">
                      <div
                        className="progress-fill"
                        style={{ width: `${change.canary_status.current_percentage}%` }}
                      />
                    </div>
                    <div className="canary-status">
                      Status: {change.canary_status.status.replace('_', ' ')}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* Create Change Modal */}
      {showCreateModal && (
        <div className="modal-overlay" onClick={() => setShowCreateModal(false)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Create New Change</h3>
              <button onClick={() => setShowCreateModal(false)} className="close-btn">×</button>
            </div>
            <form onSubmit={createChange} className="modal-body">
              <div className="form-group">
                <label>Title</label>
                <input
                  type="text"
                  required
                  value={newChange.title}
                  onChange={e => setNewChange({ ...newChange, title: e.target.value })}
                  placeholder="Brief description of the change"
                />
              </div>

              <div className="form-group">
                <label>Description</label>
                <textarea
                  value={newChange.description}
                  onChange={e => setNewChange({ ...newChange, description: e.target.value })}
                  placeholder="Detailed description of the change"
                  rows={4}
                />
              </div>

              <div className="form-row">
                <div className="form-group">
                  <label>SLO Tier</label>
                  <select
                    value={newChange.tier}
                    onChange={e => setNewChange({ ...newChange, tier: e.target.value })}
                  >
                    <option value="1">Tier 1 (99.99%) - Critical services</option>
                    <option value="2">Tier 2 (99.95%) - Important services</option>
                    <option value="3">Tier 3 (99.9%) - Internal tools</option>
                    <option value="4">Tier 4 (99.5%) - Experimental</option>
                  </select>
                </div>

                <div className="form-group">
                  <label>Change Type</label>
                  <select
                    value={newChange.change_type}
                    onChange={e => setNewChange({ ...newChange, change_type: e.target.value })}
                  >
                    <option value="deploy">Deploy</option>
                    <option value="config">Config</option>
                    <option value="infrastructure">Infrastructure</option>
                    <option value="feature">Feature</option>
                  </select>
                </div>
              </div>

              <div className="form-group">
                <label>Risk Level</label>
                <select
                  value={newChange.risk_level}
                  onChange={e => setNewChange({ ...newChange, risk_level: e.target.value })}
                >
                  <option value="low">Low</option>
                  <option value="medium">Medium</option>
                  <option value="high">High</option>
                </select>
              </div>

              <div className="form-group">
                <label>Rollback Plan</label>
                <textarea
                  value={newChange.rollback_plan}
                  onChange={e => setNewChange({ ...newChange, rollback_plan: e.target.value })}
                  placeholder="Describe how to rollback this change if something goes wrong"
                  rows={3}
                />
              </div>

              <div className="info-box">
                <AlertCircle size={16} />
                <span>This change will use canary deployment starting at 5% traffic.</span>
              </div>

              <div className="modal-footer">
                <button type="button" onClick={() => setShowCreateModal(false)} className="btn-secondary">
                  Cancel
                </button>
                <button type="submit" className="btn-primary">
                  Create Change
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Change Detail Modal */}
      {selectedChange && (
        <div className="modal-overlay" onClick={() => setSelectedChange(null)}>
          <div className="modal large" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Change Details</h3>
              <button onClick={() => setSelectedChange(null)} className="close-btn">×</button>
            </div>
            <div className="modal-body change-detail">
              <div className="detail-header">
                <div
                  className="detail-status"
                  style={{ color: getStatusColor(selectedChange.status) }}
                >
                  {selectedChange.status.replace('_', ' ')}
                </div>
                <div className="detail-tier">Tier {selectedChange.tier}</div>
                <div
                  className="detail-risk"
                  style={{ background: getRiskColor(selectedChange.risk_level) }}
                >
                  {selectedChange.risk_level}
                </div>
                <span className="detail-id">{selectedChange.id}</span>
              </div>

              <h2>{selectedChange.title}</h2>
              <p className="detail-description">{selectedChange.description}</p>

              <div className="detail-info">
                <div className="info-item">
                  <span className="info-label">Type</span>
                  <span className="info-value">{selectedChange.change_type}</span>
                </div>
                <div className="info-item">
                  <span className="info-label">Requested By</span>
                  <span className="info-value">{selectedChange.requested_by || 'N/A'}</span>
                </div>
                <div className="info-item">
                  <span className="info-label">Created</span>
                  <span className="info-value">{new Date(selectedChange.created_at).toLocaleString()}</span>
                </div>
                <div className="info-item">
                  <span className="info-label">Approval</span>
                  <span
                    className="info-value approval"
                    style={{ color: getApprovalStatusColor(selectedChange.approval_status) }}
                  >
                    {selectedChange.approval_status.replace('_', ' ')}
                  </span>
                </div>
              </div>

              {/* Canary Status */}
              {selectedChange.canary_status && (
                <div className="canary-detail">
                  <h4>Canary Deployment</h4>
                  <div className="canary-metrics">
                    <div className="canary-metric">
                      <span className="metric-label">Current Traffic</span>
                      <span className="metric-value">{selectedChange.canary_status.current_percentage}%</span>
                    </div>
                    <div className="canary-metric">
                      <span className="metric-label">Status</span>
                      <span className="metric-value">{selectedChange.canary_status.status}</span>
                    </div>
                    {selectedChange.canary_status.start_time && (
                      <div className="canary-metric">
                        <span className="metric-label">Started</span>
                        <span className="metric-value">
                          {new Date(selectedChange.canary_status.start_time).toLocaleString()}
                        </span>
                      </div>
                    )}
                  </div>
                  {selectedChange.canary_status.metrics && Object.keys(selectedChange.canary_status.metrics).length > 0 && (
                    <div className="canary-metrics-detail">
                      <h5>Live Metrics</h5>
                      {Object.entries(selectedChange.canary_status.metrics).map(([key, value]) => (
                        <div key={key} className="live-metric">
                          <span className="metric-name">{key}</span>
                          <span className="metric-value">{typeof value === 'number' ? value.toFixed(2) : value}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {selectedChange.rollback_plan && (
                <div className="detail-section">
                  <h4>Rollback Plan</h4>
                  <p>{selectedChange.rollback_plan}</p>
                </div>
              )}

              {/* Action Buttons */}
              <div className="detail-actions">
                {selectedChange.approval_status === 'pending' && (
                  <button
                    onClick={() => approveChange(selectedChange.id)}
                    className="btn-primary"
                  >
                    <CheckCircle size={16} />
                    Approve Change
                  </button>
                )}

                {selectedChange.approval_status === 'approved' && selectedChange.status === 'planned' && (
                  <button
                    onClick={() => startChange(selectedChange.id)}
                    className="btn-primary"
                  >
                    <Play size={16} />
                    Start Change
                  </button>
                )}

                {selectedChange.status === 'in_progress' && (
                  <>
                    <button
                      onClick={() => completeChange(selectedChange.id)}
                      className="btn-success"
                    >
                      <CheckCircle size={16} />
                      Complete Change
                    </button>
                    <button
                      onClick={() => rollbackChange(selectedChange.id)}
                      className="btn-danger"
                    >
                      <RotateCcw size={16} />
                      Rollback
                    </button>
                  </>
                )}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default Changes

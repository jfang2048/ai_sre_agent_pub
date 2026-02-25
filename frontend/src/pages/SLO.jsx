import { useState, useEffect } from 'react'
import { Activity, Target, AlertTriangle, TrendingUp, Clock, CheckCircle, XCircle } from 'lucide-react'
import { RadialBarChart, RadialBar, LineChart, Line, AreaChart, Area,
         XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import './SLO.css'

const API_BASE = '/api/v1'

function SLO() {
  const [sloStatus, setSloStatus] = useState(null)
  const [summary, setSummary] = useState(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetchSLOData()
    const interval = setInterval(fetchSLOData, 5000)
    return () => clearInterval(interval)
  }, [])

  const fetchSLOData = async () => {
    try {
      const [statusRes, summaryRes] = await Promise.all([
        fetch(`${API_BASE}/slo/status`),
        fetch(`${API_BASE}/slo/summary`)
      ])

      if (statusRes.ok) {
        const data = await statusRes.json()
        setSloStatus(data)
      }
      if (summaryRes.ok) {
        const data = await summaryRes.json()
        setSummary(data)
      }
      setLoading(false)
    } catch (err) {
      console.error('Failed to fetch SLO data:', err)
    }
  }

  const getErrorBudgetColor = (status) => {
    switch (status) {
      case 'ok': return '#10b981'
      case 'warning': return '#f59e0b'
      case 'critical': return '#ef4444'
      case 'exhausted': return '#7f1d1d'
      default: return '#6b7280'
    }
  }

  const getBurnRateColor = (rate) => {
    if (rate >= 5) return '#ef4444'
    if (rate >= 2) return '#f59e0b'
    return '#10b981'
  }

  const getBurnRateStatus = (rate) => {
    if (rate >= 5) return { text: 'Critical', icon: AlertTriangle }
    if (rate >= 2) return { text: 'Warning', icon: AlertTriangle }
    return { text: 'Normal', icon: CheckCircle }
  }

  const formatDuration = (hours) => {
    if (hours < 0) return '∞'
    if (hours === 0) return 'Exhausted'
    const h = Math.floor(hours)
    const m = Math.floor((hours - h) * 60)
    if (h >= 24) {
      const d = Math.floor(h / 24)
      return `${d}d ${h % 24}h`
    }
    return `${h}h ${m}m`
  }

  return (
    <div className="slo-page">
      {loading && !sloStatus ? (
        <div className="loading-state">Loading SLO data...</div>
      ) : (
        <>
          {/* SLO Summary Cards */}
          {summary && (
            <div className="slo-summary">
              <div className="summary-card">
                <AlertTriangle size={20} className="card-icon" />
                <div className="card-value">{summary.violations_count || 0}</div>
                <div className="card-label">Active Violations</div>
              </div>
              <div className="summary-card">
                <Clock size={20} className="card-icon" />
                <div className="card-value">{summary.active_incidents || 0}</div>
                <div className="card-label">Active Incidents</div>
              </div>
              <div className="summary-card">
                <TrendingUp size={20} className="card-icon" />
                <div className="card-value">{summary.active_changes || 0}</div>
                <div className="card-label">Active Changes</div>
              </div>
            </div>
          )}

          {/* SLO Details */}
          <div className="slo-details">
            {sloStatus && Object.keys(sloStatus).length > 0 ? (
              Object.entries(sloStatus).map(([sloId, slo]) => {
                const budgetColor = getErrorBudgetColor(slo.error_budget_status)
                const burnRateColor = getBurnRateColor(slo.burn_rate)
                const burnStatus = getBurnRateStatus(slo.burn_rate)
                const BurnIcon = burnStatus.icon

                // Prepare gauge data
                const gaugeData = [
                  { name: 'Achieved', value: slo.current_value || 0, fill: slo.compliant ? '#10b981' : '#ef4444' },
                  { name: 'Remaining', value: Math.max(0, 100 - (slo.current_value || 0)), fill: '#333' }
                ]

                // Prepare burn rate history data
                const burnHistory = (slo.burn_rate_history || []).slice(-20).map(h => ({
                  time: new Date(h.timestamp).toLocaleTimeString(),
                  burn: h.burn_rate
                }))

                return (
                  <div key={sloId} className="slo-card">
                    <div className="slo-card-header">
                      <h3>
                        <Target size={18} />
                        {sloId}
                      </h3>
                      <span className={`slo-badge ${slo.compliant ? 'compliant' : 'non-compliant'}`}>
                        {slo.compliant ? <CheckCircle size={14} /> : <XCircle size={14} />}
                        {slo.compliant ? 'Compliant' : 'Violation'}
                      </span>
                    </div>

                    <div className="slo-card-body">
                      {/* Current Achievement Gauge */}
                      <div className="slo-gauge-section">
                        <div className="gauge-container">
                          <ResponsiveContainer width="100%" height={160}>
                            <RadialBarChart cx="50%" cy="50%" innerRadius={50} outerRadius={80} data={gaugeData}>
                              <RadialBar dataKey="value" cornerRadius={10} />
                              <Tooltip />
                            </RadialBarChart>
                          </ResponsiveContainer>
                          <div className="gauge-center">
                            <div className="gauge-value">{(slo.current_value || 0).toFixed(2)}%</div>
                            <div className="gauge-target">Target: {slo.target}%</div>
                          </div>
                        </div>
                      </div>

                      {/* Error Budget */}
                      <div className="slo-metrics">
                        <div className="metric-row">
                          <span className="metric-label">Error Budget</span>
                          <span className="metric-value" style={{ color: budgetColor }}>
                            {slo.error_budget_remaining?.toFixed(1)}% / {slo.error_budget_status}
                          </span>
                        </div>

                        <div className="metric-row">
                          <span className="metric-label">Burn Rate</span>
                          <span className="metric-value" style={{ color: burnRateColor }}>
                            {slo.burn_rate?.toFixed(2)}x
                          </span>
                        </div>

                        <div className="metric-row">
                          <span className="metric-label">Time to Exhaustion</span>
                          <span className="metric-value">
                            {formatDuration(slo.time_to_exhaustion_hours)}
                          </span>
                        </div>

                        <div className="metric-row">
                          <span className="metric-label">Window</span>
                          <span className="metric-value">
                            {new Date(slo.window_start).toLocaleDateString()} - {new Date(slo.window_end).toLocaleDateString()}
                          </span>
                        </div>
                      </div>

                      {/* Burn Rate History */}
                      {burnHistory.length > 0 && (
                        <div className="burn-history">
                          <h4>Burn Rate History</h4>
                          <ResponsiveContainer width="100%" height={100}>
                            <LineChart data={burnHistory}>
                              <CartesianGrid strokeDasharray="3 3" stroke="#333" />
                              <XAxis dataKey="time" stroke="#888" fontSize={11} />
                              <YAxis stroke="#888" fontSize={11} />
                              <Tooltip
                                contentStyle={{ backgroundColor: '#1e1e1e', border: '1px solid #333' }}
                                labelStyle={{ color: '#fff' }}
                              />
                              <Line
                                type="monotone"
                                dataKey="burn"
                                stroke={burnRateColor}
                                strokeWidth={2}
                                dot={{ fill: burnRateColor, r: 3 }}
                              />
                            </LineChart>
                          </ResponsiveContainer>
                        </div>
                      )}
                    </div>

                    {/* Burn Rate Status Banner */}
                    <div className={`burn-rate-banner ${slo.burn_rate >= 2 ? 'warning' : 'normal'}`}>
                      <BurnIcon size={16} />
                      <span>
                        Burn rate is {slo.burn_rate?.toFixed(2)}x ({burnStatus.text})
                        {slo.burn_rate >= 5 && ' - Immediate action required!'}
                        {slo.burn_rate >= 2 && slo.burn_rate < 5 && ' - Monitor closely'}
                      </span>
                    </div>
                  </div>
                )
              })
            ) : (
              <div className="empty-state">
                <Target size={48} />
                <h3>No SLOs Configured</h3>
                <p>Add SLO definitions to track service level objectives.</p>
              </div>
            )}
          </div>

          {/* Google SRE Info */}
          <div className="sre-info">
            <h3><Activity size={18} /> Google SRE Principles</h3>
            <div className="info-grid">
              <div className="info-card">
                <h4>Error Budget</h4>
                <p>The amount of "bad" service your users can tolerate before you must improve service.</p>
                <code>Error Budget = (100 - Target) / (100 - Actual) × 100</code>
              </div>
              <div className="info-card">
                <h4>Burn Rate</h4>
                <p>How fast you're consuming your error budget relative to the acceptable rate.</p>
                <ul>
                  <li>5x or higher: Page immediately</li>
                  <li>2x or higher: Alert within 15min</li>
                  <li>1x or lower: Track for trend</li>
                </ul>
              </div>
              <div className="info-card">
                <h4>SLO Tiers</h4>
                <ul>
                  <li>Tier 1: 99.99% (43s/month error budget)</li>
                  <li>Tier 2: 99.95% (3.6min/day)</li>
                  <li>Tier 3: 99.9% (43min/day)</li>
                  <li>Tier 4: 99.5% (3.6hrs/day)</li>
                </ul>
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  )
}

export default SLO

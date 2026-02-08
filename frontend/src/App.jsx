import { useState, useEffect, useCallback } from 'react'
import { HashRouter, Routes, Route, Link, useLocation } from 'react-router-dom'
import {
  LayoutDashboard,
  Activity,
  AlertTriangle,
  GitBranch,
  Settings,
  Cpu,
  HardDrive,
  Network,
  Menu,
  X,
  Server
} from 'lucide-react'
import Dashboard from './pages/Dashboard'
import Metrics from './pages/Metrics'
import Incidents from './pages/Incidents'
import Changes from './pages/Changes'
import SLO from './pages/SLO'
import Processes from './pages/Processes'
import SystemStatus from './pages/SystemStatus'
import './App.css'

const API_BASE = '/api/v1'

// Layout component with navigation
function Layout({ children }) {
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [connected, setConnected] = useState(false)
  const location = useLocation()

  const checkConnection = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/status`)
      setConnected(res.ok)
    } catch {
      setConnected(false)
    }
  }, [])

  useEffect(() => {
    checkConnection()
    const interval = setInterval(checkConnection, 5000)
    return () => clearInterval(interval)
  }, [checkConnection])

  const navItems = [
    { path: '/', icon: LayoutDashboard, label: 'Dashboard' },
    { path: '/system-status', icon: Server, label: 'System Status' },
    { path: '/metrics', icon: Activity, label: 'Metrics' },
    { path: '/slo', icon: Activity, label: 'SLO & Error Budget' },
    { path: '/incidents', icon: AlertTriangle, label: 'Incidents' },
    { path: '/changes', icon: GitBranch, label: 'Changes' },
    { path: '/processes', icon: Cpu, label: 'Processes' },
  ]

  return (
    <div className="app">
      <nav className={`sidebar ${sidebarOpen ? 'open' : ''}`}>
        <div className="sidebar-header">
          <Activity className="logo-icon" size={28} />
          <h1>SRE Agent</h1>
          <button className="close-sidebar" onClick={() => setSidebarOpen(false)}>
            <X size={20} />
          </button>
        </div>

        <div className={`connection-status ${connected ? 'connected' : 'disconnected'}`}>
          <span className={`status-dot ${connected ? 'connected' : 'disconnected'}`}></span>
          {connected ? 'Connected' : 'Disconnected'}
        </div>

        <ul className="nav-list">
          {navItems.map((item) => {
            const Icon = item.icon
            const isActive = location.pathname === item.path
            return (
              <li key={item.path}>
                <Link
                  to={item.path}
                  className={`nav-item ${isActive ? 'active' : ''}`}
                  onClick={() => setSidebarOpen(false)}
                >
                  <Icon size={18} />
                  <span>{item.label}</span>
                </Link>
              </li>
            )
          })}
        </ul>
      </nav>

      <div className="main-content">
        <header className="top-bar">
          <button className="menu-toggle" onClick={() => setSidebarOpen(true)}>
            <Menu size={20} />
          </button>
          <div className="page-title">
            {navItems.find(i => i.path === location.pathname)?.label || 'SRE Agent'}
          </div>
          <div className="header-actions"></div>
        </header>

        <main className="content">
          {children}
        </main>
      </div>
    </div>
  )
}

function App() {
  return (
    <HashRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/system-status" element={<SystemStatus />} />
          <Route path="/metrics" element={<Metrics />} />
          <Route path="/slo" element={<SLO />} />
          <Route path="/incidents" element={<Incidents />} />
          <Route path="/changes" element={<Changes />} />
          <Route path="/processes" element={<Processes />} />
        </Routes>
      </Layout>
    </HashRouter>
  )
}

export default App

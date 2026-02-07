import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.tsx'
import './index.css'

// Add initial dark mode class
if (localStorage.getItem('dashboard-store') && JSON.parse(localStorage.getItem('dashboard-store')!).state.theme === 'dark') {
    document.documentElement.classList.add('dark');
} else {
    // Default to dark
    document.documentElement.classList.add('dark');
}

ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
        <App />
    </React.StrictMode>,
)

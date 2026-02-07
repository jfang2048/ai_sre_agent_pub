import axios from 'axios';

// Base API configuration
export const api = axios.create({
    baseURL: import.meta.env.VITE_API_URL || '/api/v1',
    headers: {
        'Content-Type': 'application/json',
    },
    timeout: 10000,
});

// Request interceptor for Auth
api.interceptors.request.use((config) => {
    const token = localStorage.getItem('sre_token');
    if (token) {
        config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
});

// Response interceptor for Errors
api.interceptors.response.use(
    (response) => response,
    (error) => {
        if (error.response?.status === 401) {
            // Redirect to Login if needed
            window.location.href = '/login';
        }
        return Promise.reject(error);
    }
);

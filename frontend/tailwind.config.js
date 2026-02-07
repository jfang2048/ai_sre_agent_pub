/** @type {import('tailwindcss').Config} */
export default {
    content: [
        "./index.html",
        "./src/**/*.{js,ts,jsx,tsx}",
    ],
    darkMode: 'class',
    theme: {
        extend: {
            colors: {
                background: '#09090b', // Zinc 950
                foreground: '#fafafa', // Zinc 50
                card: {
                    DEFAULT: '#18181b', // Zinc 900
                    foreground: '#fafafa',
                },
                primary: {
                    DEFAULT: '#6366f1', // Indigo 500
                    foreground: '#ffffff',
                },
                secondary: {
                    DEFAULT: '#27272a', // Zinc 800
                    foreground: '#fafafa',
                },
                muted: {
                    DEFAULT: '#27272a', // Zinc 800
                    foreground: '#a1a1aa', // Zinc 400
                },
                accent: {
                    DEFAULT: '#27272a', // Zinc 800
                    foreground: '#fafafa',
                },
                destructive: {
                    DEFAULT: '#ef4444', // Red 500
                    foreground: '#fafafa',
                },
                border: '#27272a', // Zinc 800
                input: '#27272a', // Zinc 800
                ring: '#6366f1', // Indigo 500
            },
        },
    },
    plugins: [],
}

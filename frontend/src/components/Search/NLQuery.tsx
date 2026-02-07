import React, { useState } from 'react';
import { Search, Loader2, Command } from 'lucide-react';

const NLQuery = () => {
    const [query, setQuery] = useState('');
    const [loading, setLoading] = useState(false);

    const handleSearch = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!query.trim()) return;

        setLoading(true);
        // Simulate AI request
        // In real app: const result = await api.post('/ai/query', { text: query });
        setTimeout(() => {
            setLoading(false);
            // Trigger dashboard update or show result
            console.log("Executed query:", query);
        }, 1500);
    };

    return (
        <form onSubmit={handleSearch} className="relative w-full max-w-lg group">
            <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none">
                <Search className="h-5 w-5 text-muted-foreground" />
            </div>
            <input
                type="text"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                className="block w-full pl-10 pr-12 py-3 border border-border rounded-xl leading-5 bg-card/50 text-foreground placeholder-muted-foreground focus:outline-none focus:bg-card focus:ring-2 focus:ring-primary focus:border-transparent transition duration-150 ease-in-out sm:text-sm shadow-lg backdrop-blur-sm"
                placeholder="Ask AI: Show CPU spikes correlated with logs..."
            />
            <div className="absolute inset-y-0 right-0 pr-3 flex items-center">
                {loading ? (
                    <Loader2 className="h-5 w-5 text-primary animate-spin" />
                ) : (
                    <div className="text-xs text-muted-foreground hidden group-focus-within:block border border-border px-1.5 py-0.5 rounded">
                        ↵ Enter
                    </div>
                )}
            </div>
        </form>
    );
};

export default NLQuery;

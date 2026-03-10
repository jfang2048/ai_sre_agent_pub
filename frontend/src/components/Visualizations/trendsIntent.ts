import type { ResourceCategory } from './ResourceProcessBreakdownPanel';

export interface TrendsNavigationIntentInput {
    collectorId?: string;
    category?: ResourceCategory;
    triggerLabel?: string;
    processFilter?: string;
    metricKey?: string;
    windowSize?: string;
}

export interface TrendsNavigationIntent extends TrendsNavigationIntentInput {
    requestToken: number;
}

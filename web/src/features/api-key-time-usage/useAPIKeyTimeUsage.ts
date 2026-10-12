import { useEffect, useState } from 'react';
import { ApiError } from '@/lib/api';
import { fetchAPIKeyTimeUsage } from './api';
import type { TimeUsageQuery, TimeUsageReport } from './types';

interface State {
  request: string;
  loading: boolean;
  data: TimeUsageReport | null;
  error: Error | null;
}
export function useAPIKeyTimeUsage(query: TimeUsageQuery, valid: boolean, refresh: string, onAuthRequired?: () => void) {
  const { start, end, apiKeyId } = query;
  const request = JSON.stringify([start, end, apiKeyId, refresh]);
  const [state, setState] = useState<State>({ request: '', loading: true, data: null, error: null });
  useEffect(() => {
    if (!valid) return;
    const controller = new AbortController();
    const load = async () => {
      // Keep the previous range on screen until this request settles. An invalid
      // range never reaches here, and a rejected range is not retried automatically.
      setState(previous => ({ request, loading: true, data: previous.data, error: null }));
      try {
        const data = await fetchAPIKeyTimeUsage({ start, end, apiKeyId }, controller.signal);
        if (controller.signal.aborted) return;
        setState({ request, loading: false, data, error: null });
      } catch (error) {
        if (controller.signal.aborted) return;
        setState({ request, loading: false, data: null, error: error instanceof Error ? error : new Error('api_key_time_usage_failed') });
        if (error instanceof ApiError && error.status === 401) onAuthRequired?.();
      }
    };
    void load();
    return () => controller.abort();
  }, [apiKeyId, end, onAuthRequired, request, start, valid]);
  const current = valid && request === state.request;
  return { data: state.data, error: current ? state.error : null, loading: valid && (!current || state.loading) };
}

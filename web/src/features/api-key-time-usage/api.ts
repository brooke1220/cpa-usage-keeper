import { ApiError, apiFetch, apiPath } from '@/lib/api';
import type { TimeUsageQuery, TimeUsageReport } from './types';

export async function fetchAPIKeyTimeUsage(query: TimeUsageQuery, signal?: AbortSignal): Promise<TimeUsageReport> {
  const params = new URLSearchParams({ start_date: query.start, end_date: query.end });
  if (query.apiKeyId) params.set('api_key_id', query.apiKeyId);
  const response = await apiFetch(apiPath(`/usage/api-key-time-usage?${params}`), { signal, credentials: 'include', cache: 'no-store' });
  if (!response.ok) {
    let message = 'api_key_time_usage_failed';
    try {
      const body = await response.json() as { error?: string };
      message = body.error || message;
    } catch { /* Retain a stable error for non-JSON responses. */ }
    throw new ApiError(message, response.status);
  }
  return response.json() as Promise<TimeUsageReport>;
}

export interface WorkHoursSchedule {
  timezone: string;
  start: string;
  end: string;
  source: 'saved' | 'default';
}

export async function fetchWorkHours(signal?: AbortSignal): Promise<WorkHoursSchedule> {
  const response = await apiFetch(apiPath('/usage/work-hours'), { signal, credentials: 'include', cache: 'no-store' });
  if (!response.ok) {
    let message = 'work_hours_failed';
    try {
      const body = await response.json() as { error?: string };
      message = body.error || message;
    } catch { /* Retain a stable error for non-JSON responses. */ }
    throw new ApiError(message, response.status);
  }
  return response.json() as Promise<WorkHoursSchedule>;
}

export async function updateWorkHours(start: string, end: string): Promise<WorkHoursSchedule> {
  const response = await apiFetch(apiPath('/usage/work-hours'), {
    method: 'PUT',
    credentials: 'include',
    cache: 'no-store',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ start, end }),
  });
  if (!response.ok) {
    let message = 'work_hours_failed';
    try {
      const body = await response.json() as { error?: string };
      message = body.error || message;
    } catch { /* Retain a stable error for non-JSON responses. */ }
    throw new ApiError(message, response.status);
  }
  return response.json() as Promise<WorkHoursSchedule>;
}

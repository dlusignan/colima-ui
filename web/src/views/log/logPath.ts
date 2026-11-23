/** Link to the operation log of a job. The dashboard and the detail page use it. */
export function logPath(profile: string, jobId?: string | null): string {
  const base = `/profiles/${encodeURIComponent(profile)}/log`
  return jobId ? `${base}?job=${encodeURIComponent(jobId)}` : base
}

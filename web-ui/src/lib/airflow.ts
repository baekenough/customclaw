const AIRFLOW_API_URL =
  process.env.AIRFLOW_API_URL || "http://airflow:8080/api/v2";

let cachedToken: { token: string; expiresAt: number } | null = null;

async function getAirflowToken(): Promise<string> {
  if (cachedToken && Date.now() < cachedToken.expiresAt) {
    return cachedToken.token;
  }
  const baseUrl = AIRFLOW_API_URL.replace("/api/v2", "");
  const res = await fetch(`${baseUrl}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      username: process.env.AIRFLOW_API_USER,
      password: process.env.AIRFLOW_API_PASSWORD,
    }),
    cache: "no-store",
  });
  if (!res.ok) throw new Error(`Airflow auth failed: ${res.status}`);
  const data = await res.json();
  cachedToken = { token: data.access_token, expiresAt: Date.now() + 5 * 60 * 1000 };
  return data.access_token;
}

export async function airflowFetch(path: string, options?: RequestInit) {
  const token = await getAirflowToken();
  return fetch(`${AIRFLOW_API_URL}${path}`, {
    ...options,
    cache: "no-store",
    headers: {
      ...options?.headers,
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
  });
}

export interface AirflowHealth {
  status: boolean;
  metadatabase?: boolean;
  scheduler?: boolean;
  dagProcessor?: boolean;
}

interface AirflowHealthResponse {
  metadatabase?: { status?: string };
  scheduler?: { status?: string };
  dag_processor?: { status?: string };
}

function parseAirflowHealthResponse(data: AirflowHealthResponse): AirflowHealth {
  const metadatabase = data.metadatabase?.status === "healthy";
  const scheduler = data.scheduler?.status === "healthy";
  const dagProcessor = data.dag_processor?.status === "healthy";
  return {
    status: metadatabase && scheduler && dagProcessor,
    metadatabase,
    scheduler,
    dagProcessor,
  };
}

export async function checkAirflowHealth(): Promise<AirflowHealth> {
  const baseUrl = AIRFLOW_API_URL.replace("/api/v2", "");

  // Attempt 1: API health endpoint with token auth
  try {
    const res = await airflowFetch("/monitor/health");
    if (res.ok) {
      const data: AirflowHealthResponse = await res.json();
      return parseAirflowHealthResponse(data);
    }
  } catch {
    // Auth or network failure — fall through to direct endpoint
  }

  // Attempt 2: Direct health endpoint (no auth required on some setups)
  try {
    const res = await fetch(`${baseUrl}/api/v2/monitor/health`, {
      cache: "no-store",
      signal: AbortSignal.timeout(5000),
    });
    if (res.ok) {
      const data: AirflowHealthResponse = await res.json();
      return parseAirflowHealthResponse(data);
    }
  } catch {
    // Both attempts failed
  }

  return { status: false };
}

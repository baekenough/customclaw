import { auth } from "@/lib/auth";
import { airflowFetch } from "@/lib/airflow";

export async function GET() {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }

  try {
    const res = await airflowFetch("/dags");
    if (!res.ok) {
      return Response.json(
        { error: `Airflow error: ${res.status}` },
        { status: res.status }
      );
    }
    const data = await res.json();

    // Enrich each DAG with its latest run state.
    // Airflow v2 /dags does not include last_run_state, which the frontend
    // needs to compute running/failed/success counts in computeDagSummary().
    const enrichedDags = await Promise.all(
      (data.dags ?? []).map(async (dag: Record<string, unknown>) => {
        try {
          const runRes = await airflowFetch(
            `/dags/${dag.dag_id}/dagRuns?limit=1&order_by=-start_date`
          );
          if (!runRes.ok) return { ...dag, last_run: null, last_run_state: null };
          const runData = await runRes.json();
          const lastRun = runData.dag_runs?.[0];
          return {
            ...dag,
            last_run: lastRun?.start_date ?? null,
            last_run_state: lastRun?.state ?? null,
          };
        } catch {
          return { ...dag, last_run: null, last_run_state: null };
        }
      })
    );

    return Response.json({ ...data, dags: enrichedDags });
  } catch (error) {
    console.error("GET /api/airflow/dags error:", error);
    return Response.json({ error: "Failed to reach Airflow" }, { status: 503 });
  }
}

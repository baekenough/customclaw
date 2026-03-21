import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { airflowFetch } from "@/lib/airflow";

export async function POST(
  request: NextRequest,
  { params }: { params: Promise<{ dagId: string }> }
) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }
  try {
    const { dagId } = await params;
    const body = await request.json().catch(() => ({}));
    const res = await airflowFetch(`/dags/${dagId}/dagRuns`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        dag_run_id: `web_ui_${Date.now()}`,
        logical_date: new Date().toISOString(),
        ...body,
      }),
    });
    if (!res.ok) {
      return Response.json(
        { error: `Airflow error: ${res.status}` },
        { status: res.status }
      );
    }
    return Response.json(await res.json());
  } catch (error) {
    console.error("POST trigger error:", error);
    return Response.json({ error: "Failed" }, { status: 503 });
  }
}

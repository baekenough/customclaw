import { NextRequest } from "next/server";
import { auth } from "@/lib/auth";
import { airflowFetch } from "@/lib/airflow";

export async function GET(
  _request: NextRequest,
  { params }: { params: Promise<{ dagId: string; runId: string }> }
) {
  const session = await auth();
  if (!session) {
    return Response.json({ error: "Unauthorized" }, { status: 401 });
  }
  try {
    const { dagId, runId } = await params;
    const res = await airflowFetch(
      `/dags/${dagId}/dagRuns/${encodeURIComponent(runId)}/taskInstances`
    );
    if (!res.ok) {
      return Response.json(
        { error: `Airflow error: ${res.status}` },
        { status: res.status }
      );
    }
    return Response.json(await res.json());
  } catch (error) {
    console.error("GET task instances error:", error);
    return Response.json({ error: "Failed" }, { status: 503 });
  }
}
